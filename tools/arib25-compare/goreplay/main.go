// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// goreplay は replay.c の Go 版。recut100b --card-log の記録をカードの代わりにして、
// B25 を通す前の TS を internal/b25 で外す。
//
//	goreplay card.log in.ts out.ts
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/waya0125/recfriio/internal/b25"
)

type card struct {
	responses map[string][]byte
	init      []byte
}

func (c *card) SystemKey() []byte { return c.init[16:48] }
func (c *card) InitCBC() []byte   { return c.init[48:56] }
func (c *card) CASystemID() int   { return int(binary.BigEndian.Uint16(c.init[6:])) }

func (c *card) ProcessECM(ecm []byte) ([]byte, int, error) {
	cmd := append(append([]byte{0x90, 0x34, 0x00, 0x00, byte(len(ecm))}, ecm...), 0)
	r, ok := c.responses[hex.EncodeToString(cmd)]
	if !ok || len(r) < 25 {
		return nil, 0, errors.New("記録に無い ECM")
	}
	return r[6:22], int(binary.BigEndian.Uint16(r[4:])), nil
}

func load(path string) (*card, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c := &card{responses: map[string][]byte{}}
	var cmd string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<16), 1<<16)
	for s.Scan() {
		line := s.Text()
		switch {
		case strings.HasPrefix(line, "> "):
			cmd = line[2:]
		case strings.HasPrefix(line, "< "):
			r, err := hex.DecodeString(line[2:])
			if err != nil {
				return nil, err
			}
			if _, seen := c.responses[cmd]; !seen {
				c.responses[cmd] = r
			}
			if cmd == "9030000000" {
				c.init = r
			}
		}
	}
	if len(c.init) < 57 {
		return nil, errors.New("記録に初期設定の答えが無い")
	}
	return c, s.Err()
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: goreplay card.log in.ts out.ts")
		os.Exit(2)
	}
	c, err := load(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	in, err := os.Open(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := os.Create(os.Args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	d := b25.New(c, 4, false)
	buf := make([]byte, 65536)
	errorsSeen := 0
	for {
		n, err := io.ReadFull(in, buf)
		if n > 0 {
			o, perr := d.Put(buf[:n])
			if perr != nil {
				errorsSeen++
			}
			out.Write(o)
		}
		if err != nil {
			break
		}
	}
	o, err := d.Flush()
	if err != nil {
		errorsSeen++
	}
	out.Write(o)
	if err := out.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "go: errors %d\n", errorsSeen)
	if errorsSeen > 0 {
		os.Exit(1)
	}
}
