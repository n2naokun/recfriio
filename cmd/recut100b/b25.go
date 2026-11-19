// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/waya0125/recfriio/internal/asie5606"
	"github.com/waya0125/recfriio/internal/b25"
	"github.com/waya0125/recfriio/internal/bcas"
	"github.com/waya0125/recfriio/internal/ut100b"
)

type decoder struct {
	*b25.Decoder
	closers []io.Closer
}

func (d *decoder) Close() {
	for _, c := range d.closers {
		c.Close()
	}
	d.closers = nil
}

func newDecoder(o *options, dev *ut100b.Device, log io.Writer) (b25Decoder, error) {
	var transport bcas.Transport
	d := &decoder{}
	if o.pcsc {
		reader, err := openPCSC(log)
		if err != nil {
			return nil, err
		}
		d.closers = append(d.closers, reader)
		transport = reader
	} else {
		fmt.Fprintln(log, "B-CAS: built-in slot of the tuner")
		reader := asie5606.NewReader(dev.Bridge, dev.Tuner.CardAddr)
		reader.Log = log
		transport = reader
	}
	if o.cardLog != "" {
		file, err := os.OpenFile(o.cardLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			d.Close()
			return nil, err
		}
		d.closers = append(d.closers, file)
		transport = &loggingTransport{t: transport, w: file}
	}
	card, err := bcas.Open(transport)
	if err != nil {
		d.Close()
		return nil, err
	}
	d.Decoder = b25.New(card, o.round, o.strip)
	fmt.Fprintln(log, "B25Decoder initialized.")
	return d, nil
}

// loggingTransport はカードとのやり取りを 1 行ずつ書く（"> 送ったもの" と "< 答え"）。
// 照合の道具（cmd/b25replay）が読み戻す。
type loggingTransport struct {
	t bcas.Transport
	w io.Writer
}

func (l *loggingTransport) Reset() error {
	fmt.Fprintln(l.w, "reset")
	return l.t.Reset()
}

func (l *loggingTransport) Transmit(apdu []byte) ([]byte, error) {
	r, err := l.t.Transmit(apdu)
	if err != nil {
		fmt.Fprintf(l.w, "> %s\n! %v\n", hex.EncodeToString(apdu), err)
		return r, err
	}
	fmt.Fprintf(l.w, "> %s\n< %s\n", hex.EncodeToString(apdu), hex.EncodeToString(r))
	return r, nil
}
