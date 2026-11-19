// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// ut100b-firmware は QRS-UT100B のファームを付属 CD のドライバーから取り出し、ファームの無い UT100B に送る。
//
//	ut100b-firmware extract <Quixun_AS11Loader.sys> [-o asv5211-firmware.bin] [--old]
//	ut100b-firmware load [asv5211-firmware.bin] [--index 0x5399]
//
// 送るのは Linux の usbfs（/dev/bus/usb）に直接。udev で 1738:5211 に書き込み権限を与えるか root で。
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/waya0125/recfriio/internal/firmware"
	"github.com/waya0125/recfriio/internal/usbfs"
	"github.com/waya0125/recfriio/internal/ut100b"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "extract":
		err = extract(os.Args[2:])
	case "load":
		err = load(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `使い方:
  ut100b-firmware extract <Quixun_AS11Loader.sys> [-o asv5211-firmware.bin] [--old]
      付属 CD の Driver フォルダーの Quixun_AS11Loader.sys からファームを取り出す
  ut100b-firmware load [asv5211-firmware.bin] [--index 0x5399]
      ファームの無い UT100B（1738:5211）にファームを送る`)
	os.Exit(2)
}

func extract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	output := fs.String("o", "asv5211-firmware.bin", "書き出す先")
	old := fs.Bool("old", false, "主のファームではなく、古い版（送られないもの）を取り出す")
	fs.Parse(reorder(fs, args))
	if fs.NArg() != 1 {
		usage()
	}
	loader, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	which := 0
	if *old {
		which = 1
	}
	image, index, info, name, err := firmware.Extract(loader, which)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*output, image, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s\n  → %s: %s、起動アドレス %#04x、SHA-256 %s\n", name, *output, info.Name, index, firmware.Digest(image))
	return nil
}

func load(args []string) error {
	fs := flag.NewFlagSet("load", flag.ExitOnError)
	indexFlag := fs.String("index", "", "起動アドレス（知らないファームのときだけ）")
	fs.Parse(reorder(fs, args))
	path := "asv5211-firmware.bin"
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}
	image, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !firmware.LooksLikeFirmware(image) {
		return fmt.Errorf("%s は ASV52xx のファームの形をしていない", path)
	}
	index, known := firmware.IndexFor(image)
	if *indexFlag != "" {
		v, err := strconv.ParseUint(*indexFlag, 0, 16)
		if err != nil {
			return err
		}
		if known && uint16(v) != index {
			fmt.Fprintf(os.Stderr, "注意: このファームの起動アドレスは %#04x のはずだが、%#04x で送る\n", index, v)
		}
		index, known = uint16(v), true
	}
	if !known {
		return fmt.Errorf("知らないファーム。起動アドレスを --index で指定する（違うと抜き差しが要る）")
	}
	if cold, _ := usbfs.Find(ut100b.ColdID); len(cold) == 0 {
		if warm, _ := usbfs.Find(ut100b.WarmID); len(warm) > 0 {
			fmt.Println("UT100B はもうファームが入った状態（3275:7020）。入れ直すには抜き差しする。")
			return nil
		}
	}
	dev, err := firmware.Load(image, index, ut100b.ColdID, ut100b.WarmID, 5*time.Second)
	if err != nil {
		return err
	}
	fmt.Printf("送った。%s が 3275:7020 として出直した（起動アドレス %#04x）。\n", dev, index)
	return nil
}

// reorder はオプションを位置の引数より前に並べ替える（flag は最初の位置の引数で止まるため）。
func reorder(fs *flag.FlagSet, args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			name := a[1:]
			if len(name) > 0 && name[0] == '-' {
				name = name[1:]
			}
			if f := fs.Lookup(name); f != nil && !isBool(f) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		rest = append(rest, a)
	}
	return append(flags, rest...)
}

func isBool(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}
