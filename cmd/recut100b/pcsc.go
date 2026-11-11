// SPDX-License-Identifier: MIT
// Copyright (c) 2026 KASHIWAYA Rikuto

package main

import (
	"io"
)

type pcscReader interface {
	Reset() error
	Transmit(apdu []byte) ([]byte, error)
	io.Closer
}

func openPCSC(log io.Writer) (pcscReader, error) { return nil, errNoPCSC }
