// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// recut100b は QRS-UT100B で地上波を受けて TS を書き出す。
// 引数の形は recfriio・recpt1 と同じ（チャンネル 秒 出力先）。
//
//	recut100b [オプション] チャンネル 秒 出力先
//	  チャンネル: 13〜62（UHF）、C13〜C63（CATV。K13 とも書ける）
//	  秒:         - なら止められるまで
//	  出力先:     - なら標準出力
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/waya0125/recfriio/internal/firmware"
	"github.com/waya0125/recfriio/internal/ut100b"
)

const usage = `使い方: recut100b [オプション] チャンネル 秒 出力先
  チャンネル  13〜62（UHF）、C13〜C63（CATV。K13 とも書ける）
  秒          録る長さ。- なら止められるまで
  出力先      書き出すファイル。- なら標準出力

オプション:
`

// signalLevelRetry は、受け始めの C/N がこれより低ければ読み直す（recfriio と同じ）。
const (
	signalLevelThreshold = 10.0
	signalLevelRetries   = 2
	signalLevelInterval  = 400 * time.Millisecond
)

type options struct {
	b25      bool
	pcsc     bool
	round    int
	strip    bool
	sync     bool
	device   string
	raw      string
	cardLog  string
	firmware string
	channel  int
	catv     bool
	seconds  int // 0 なら止められるまで
	output   string
	freqMHz  int
	exitCode int
}

func parseArgs(args []string, stderr io.Writer) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet("recut100b", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	fs.BoolVar(&o.b25, "b25", false, "B-CAS カードでスクランブルを外す（ARIB STD-B25）。カードは内蔵のスロットのもの")
	fs.BoolVar(&o.pcsc, "pcsc", false, "--b25 で、内蔵のスロットの代わりに PC/SC のカードリーダーを使う")
	fs.IntVar(&o.round, "round", 4, "Multi2 の段数")
	fs.BoolVar(&o.strip, "strip", false, "ヌルパケットを捨てる（UT100B はもともと出さない）")
	fs.BoolVar(&o.sync, "sync", false, "--b25 で、鍵がそろうまでの失敗を待つ")
	fs.StringVar(&o.device, "device", "", "使う UT100B の機器ファイル（/dev/bus/usb/BBB/DDD）。空なら空いているもの")
	fs.StringVar(&o.firmware, "firmware", "", "ファームの無い UT100B（1738:5211）しか無ければ、このファームを送ってから使う")
	fs.StringVar(&o.raw, "raw", "", "B25 を通す前の TS も書き出す（照合用）")
	fs.StringVar(&o.cardLog, "card-log", "", "B-CAS カードとのやり取りを書き出す（照合用。鍵が入るので人に渡さない）")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() != 3 {
		fs.Usage()
		return nil, errors.New("チャンネル・秒・出力先の 3 つを渡す")
	}
	ch := strings.ToUpper(fs.Arg(0))
	if strings.HasPrefix(ch, "C") || strings.HasPrefix(ch, "K") {
		o.catv = true
		ch = ch[1:]
	}
	n, err := strconv.Atoi(ch)
	if err != nil {
		return nil, fmt.Errorf("チャンネル %q が読めない", fs.Arg(0))
	}
	o.channel = n
	if o.freqMHz, err = ut100b.Frequency(o.channel, o.catv); err != nil {
		return nil, err
	}
	if fs.Arg(1) != "-" {
		if o.seconds, err = strconv.Atoi(fs.Arg(1)); err != nil || o.seconds <= 0 {
			return nil, fmt.Errorf("秒 %q が読めない", fs.Arg(1))
		}
	}
	o.output = fs.Arg(2)
	if o.pcsc && !o.b25 {
		return nil, errors.New("--pcsc は --b25 と一緒に使う")
	}
	return o, nil
}

func main() {
	o, err := parseArgs(os.Args[1:], os.Stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
	if err := run(o, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(o.exitCode)
}

func run(o *options, log io.Writer) error {
	dev, err := ut100b.OpenDevice(o.device)
	if errors.Is(err, ut100b.ErrNoDevice) && o.firmware != "" && o.device == "" {
		if lerr := loadFirmware(o.firmware, log); lerr != nil {
			return lerr
		}
		dev, err = ut100b.OpenDevice("")
	}
	if err != nil {
		return err
	}
	defer dev.Close()
	fmt.Fprintf(log, "device: %s\n", dev.USB.Path())
	dev.Tuner.Log = log

	if err := dev.Tuner.Open(); err != nil {
		return err
	}
	if err := dev.Tuner.Tune(o.freqMHz); err != nil {
		return err
	}
	for i := 0; i < signalLevelRetries; i++ {
		cnr, raw, err := dev.Tuner.CNR()
		if err != nil {
			return err
		}
		fmt.Fprintf(log, "CNR: %.4f dB (raw %#x)\n", cnr, raw)
		if cnr >= signalLevelThreshold {
			break
		}
		time.Sleep(signalLevelInterval)
	}

	var decoder b25Decoder
	if o.b25 {
		decoder, err = newDecoder(o, dev, log)
		if err != nil {
			// recfriio と同じく、B25 なしで続け、終わりのコードを 1 にする
			fmt.Fprintln(log, err)
			fmt.Fprintln(log, "disable b25 decoding.")
			decoder = nil
			o.exitCode = 1
		} else {
			defer decoder.Close()
		}
	}

	var out io.Writer = os.Stdout
	if o.output != "-" {
		file, err := os.Create(o.output)
		if err != nil {
			return err
		}
		defer file.Close()
		out = file
	}
	var raw *os.File
	if o.raw != "" {
		if raw, err = os.Create(o.raw); err != nil {
			return err
		}
		defer raw.Close()
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGPIPE)
	defer signal.Stop(signals)

	fmt.Fprintln(log, "Output ts file.")
	start := time.Now()
	var deadline <-chan time.Time
	if o.seconds > 0 {
		timer := time.NewTimer(time.Duration(o.seconds) * time.Second)
		defer timer.Stop()
		deadline = timer.C
	}

	stream, err := dev.StartStream()
	if err != nil {
		return err
	}
	aligner := ut100b.NewAligner()
	aligner.Log = log
	b25Errors := 0
	synced := false
	write := func(data []byte) error {
		if len(data) == 0 {
			return nil
		}
		_, err := out.Write(data)
		return err
	}

	var writeErr error
loop:
	for {
		select {
		case <-deadline:
			break loop
		case s := <-signals:
			fmt.Fprintf(log, "interrupted (%v).\n", s)
			break loop
		case chunk, ok := <-stream.Chunks:
			if !ok {
				fmt.Fprintln(log, "the USB stream ended.")
				break loop
			}
			packets := aligner.Feed(chunk)
			if len(packets) == 0 {
				// 受け始めの捨てている間など。空のまま B25 に渡すと「TS ではない入力」で失敗を数えてしまう
				continue
			}
			if raw != nil {
				if _, err := raw.Write(packets); err != nil {
					writeErr = err
					break loop
				}
			}
			if decoder != nil {
				decoded, err := decoder.Put(packets)
				if err != nil {
					if !synced && o.sync {
						fmt.Fprintln(log, "Wait for B25 sync")
						continue
					}
					b25Errors++
					if b25Errors == b25ErrorMax {
						fmt.Fprintf(log, "B25 Error: %v\n", err)
						fmt.Fprintln(log, "Continue recording without B25.")
						decoder.Close()
						decoder = nil
						o.exitCode = 1
					}
					continue
				}
				if len(decoded) > 0 {
					synced = true
				}
				packets = decoded
			}
			if err := write(packets); err != nil {
				writeErr = err
				break loop
			}
		}
	}
	stream.Stop()
	elapsed := time.Since(start)
	if stream.Dropped > 0 || stream.Errors > 0 {
		fmt.Fprintf(log, "USB: dropped %d chunks, %d failed transfers (last: %v)\n", stream.Dropped, stream.Errors, stream.LastError)
	}
	if decoder != nil && writeErr == nil {
		rest, err := decoder.Flush()
		if err != nil {
			fmt.Fprintf(log, "B25 Error: %v\n", err)
			o.exitCode = 1
		}
		writeErr = write(rest)
	}
	if writeErr != nil {
		fmt.Fprintf(log, "write failed: %v\n", writeErr)
		o.exitCode = 1
	}
	fmt.Fprintln(log, "done.")
	fmt.Fprintf(log, "Rec time: %.6f sec.\n", elapsed.Seconds())
	if cnr, raw, err := dev.Tuner.CNR(); err == nil {
		fmt.Fprintf(log, "CNR: %.4f dB (raw %#x)\n", cnr, raw)
	}
	return nil
}

func loadFirmware(path string, log io.Writer) error {
	image, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	index, ok := firmware.IndexFor(image)
	if !ok {
		return fmt.Errorf("%s は知らないファーム（ut100b-firmware load --index で送る）", path)
	}
	fmt.Fprintf(log, "loading the firmware %s...\n", path)
	dev, err := firmware.Load(image, index, ut100b.ColdID, ut100b.WarmID, 5*time.Second)
	if err != nil {
		return err
	}
	fmt.Fprintf(log, "the firmware is running: %s\n", dev)
	return nil
}

// b25ErrorMax 回失敗したら、B25 をやめて録り続ける（recfriio と同じ）。
const b25ErrorMax = 100

// b25Decoder は B25 の復号器（b25.go）。
type b25Decoder interface {
	Put(packets []byte) ([]byte, error)
	Flush() ([]byte, error)
	Close()
}
