// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// recmtvu3s は SKNET MonsterTV U3S（3275:9015）で地デジ・BS・CS を受けて TS を書き出す。
// 引数の形は recut100b・recfriio・recpt1 と同じ（チャンネル 秒 出力先）。
//
//	recmtvu3s [オプション] チャンネル 秒 出力先
//	  チャンネル: 13〜62（UHF）、BS15_0 など、CS2〜CS24
//	  秒:         - なら止められるまで
//	  出力先:     - なら標準出力
//
// EM28178 が USB 転送でかける暗号は外さない（mtvu3s.Descrambler を参照）。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/waya0125/recut100b/internal/mtvu3s"
	"github.com/waya0125/recut100b/internal/tscheck"
)

const usage = `使い方: recmtvu3s [オプション] チャンネル 秒 出力先
  チャンネル  13〜62（UHF）、BS01_0〜BS23_7（BS）、CS2〜CS24（110 度 CS）
  秒          録る長さ。- なら止められるまで
  出力先      書き出すファイル。- なら標準出力

オプション:
`

// 受け始めの C/N がこれより低ければ読み直す（recut100b・recfriio と同じ）。
const (
	signalLevelThreshold = 10.0
	signalLevelRetries   = 2
	signalLevelInterval  = 400 * time.Millisecond
)

type options struct {
	b25      bool
	round    int
	strip    bool
	sync     bool
	stats    bool
	device   string
	raw      string
	cardLog  string
	channel  mtvu3s.Channel
	seconds  int // 0 なら止められるまで
	output   string
	exitCode int
}

func parseArgs(args []string, stderr io.Writer) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet("recmtvu3s", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	fs.BoolVar(&o.b25, "b25", false, "B-CAS カードでスクランブルを外す（ARIB STD-B25）。U3S にはスロットが無いので PC/SC のカードリーダーを使う")
	pcsc := fs.Bool("pcsc", false, "--b25 と同じ（recut100b と書き方を揃えるため。U3S はいつも PC/SC）")
	fs.IntVar(&o.round, "round", 4, "Multi2 の段数")
	fs.BoolVar(&o.strip, "strip", false, "ヌルパケットを捨てる")
	fs.BoolVar(&o.sync, "sync", false, "--b25 で、鍵がそろうまでの失敗を待つ")
	fs.BoolVar(&o.stats, "stats", false, "終わりに TS のヘッダーを数えた結果（同期・TEI・連続カウンター・PID）を書く")
	fs.StringVar(&o.device, "device", "", "使う U3S の機器ファイル（/dev/bus/usb/BBB/DDD）。空なら空いているもの")
	fs.StringVar(&o.raw, "raw", "", "B25 を通す前の TS も書き出す（照合用）")
	fs.StringVar(&o.cardLog, "card-log", "", "B-CAS カードとのやり取りを書き出す（照合用。鍵が入るので人に渡さない）")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() != 3 {
		fs.Usage()
		return nil, errors.New("チャンネル・秒・出力先の 3 つを渡す")
	}
	ch, err := mtvu3s.ParseChannel(fs.Arg(0))
	if err != nil {
		return nil, err
	}
	o.channel = ch
	if fs.Arg(1) != "-" {
		if o.seconds, err = strconv.Atoi(fs.Arg(1)); err != nil || o.seconds <= 0 {
			return nil, fmt.Errorf("秒 %q が読めない", fs.Arg(1))
		}
	}
	o.output = fs.Arg(2)
	if *pcsc {
		o.b25 = true
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
	dev, err := mtvu3s.OpenDevice(o.device)
	if err != nil {
		return err
	}
	defer dev.Close()
	fmt.Fprintf(log, "device: %s\n", dev.USB.Path())
	dev.Log = log

	if err := dev.Init(); err != nil {
		return err
	}
	fmt.Fprintf(log, "channel: %s (%s, %d kHz)\n", o.channel.Name, o.channel.Band, o.channel.FreqKHz)
	if _, err := dev.Tune(o.channel); err != nil {
		return err
	}
	for range signalLevelRetries {
		cnr, raw, err := dev.CNR()
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
		decoder, err = newDecoder(o, log)
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
	aligner := mtvu3s.NewAligner()
	aligner.Log = log
	descrambler, err := mtvu3s.NewDescrambler([16]byte{})
	if err != nil {
		return err
	}
	aligner.Descrambler = descrambler
	var stats *tscheck.Stats
	if o.stats {
		stats = tscheck.New()
	}
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
			if stats != nil {
				stats.Add(packets)
			}
			if raw != nil && len(packets) > 0 {
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
	if err := dev.StopStream(stream); err != nil {
		fmt.Fprintf(log, "stop: %v\n", err)
	}
	elapsed := time.Since(start)
	if stream.Dropped > 0 || stream.Errors > 0 || stream.FrameErrors > 0 {
		fmt.Fprintf(log, "USB: dropped %d chunks, %d failed transfers, %d failed frames (last: %v)\n",
			stream.Dropped, stream.Errors, stream.FrameErrors, stream.LastError)
	}
	if aligner.Resyncs > 0 || aligner.BadSync > 0 {
		fmt.Fprintf(log, "TS: %d resyncs, %d packets without 0x47, %d bytes skipped\n", aligner.Resyncs, aligner.BadSync, aligner.Skipped)
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
	if cnr, raw, err := dev.CNR(); err == nil {
		fmt.Fprintf(log, "CNR: %.4f dB (raw %#x)\n", cnr, raw)
	}
	if stats != nil {
		stats.Print(log, 8)
	}
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
