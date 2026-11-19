// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package asie5606

import (
	"bytes"
	"math/bits"
	"testing"
	"time"
)

// fakeASIE は ASIE5606 とカードの作り物。窓に書かれたブロックを T=1 として読み、答えを窓に置く。
type fakeASIE struct {
	t        *testing.T
	regs     map[uint8]uint8
	tx       [0x140]byte
	rx       []byte
	atr      []byte
	answer   func(apdu []byte) []byte
	pending  []byte // 続きのブロックで返す残り
	ns       uint8  // カードの次の I ブロックの番号
	received [][]byte
	noCard   bool
}

func newFakeASIE(t *testing.T) *fakeASIE {
	return &fakeASIE{t: t, regs: map[uint8]uint8{regVersion: 0x06, regStatus: statusCardPresent},
		atr: []byte{0x3b, 0xf0, 0x12, 0x00, 0xff, 0x91, 0x81, 0xb1, 0x7c, 0x45, 0x1f, 0x03, 0x99}}
}

func reverse(p []byte) []byte {
	out := make([]byte, len(p))
	for i, b := range p {
		out[i] = bits.Reverse8(b)
	}
	return out
}

func (f *fakeASIE) RegRead(addr, reg uint8) (uint8, bool, error) {
	switch {
	case reg == regReceiveLow:
		return uint8(len(f.rx)), true, nil
	case reg == regReceiveHigh:
		return uint8(len(f.rx) >> 8), true, nil
	case reg == regStatus:
		v := f.regs[regStatus] &^ statusCardPresent
		if !f.noCard {
			v |= statusCardPresent
		}
		return v, true, nil
	case reg >= regWindow && reg < regWindow+windowSize:
		i := int(f.regs[regWindowPage])*windowSize + int(reg-regWindow)
		return f.rx[i], true, nil
	}
	return f.regs[reg], true, nil
}

func (f *fakeASIE) RegWrite(addr, reg, value uint8) (bool, error) {
	switch {
	case reg >= regWindow && reg < regWindow+windowSize:
		f.tx[int(f.regs[regWindowPage])*windowSize+int(reg-regWindow)] = value
		return true, nil
	case reg == regCommand:
		f.command(value)
		return true, nil
	}
	f.regs[reg] = value
	return true, nil
}

func (f *fakeASIE) reply(pcb uint8, info []byte) {
	block := append([]byte{0x00, pcb, uint8(len(info))}, info...)
	var lrc byte
	f.rx = reverse(block)
	for _, b := range f.rx {
		lrc ^= b
	}
	f.rx = append(f.rx, lrc)
}

func (f *fakeASIE) sendPart() {
	n := min(len(f.pending), maxInformation)
	pcb := f.ns << 6
	if n < len(f.pending) {
		pcb |= 0x20
	}
	f.reply(pcb, f.pending[:n])
	f.pending = f.pending[n:]
	f.ns ^= 1
}

func (f *fakeASIE) command(op uint8) {
	switch op {
	case cmdReset:
		f.rx = reverse(f.atr)
	case cmdAcknowledge:
		f.rx = nil
	case cmdTransmit:
		length := int(f.regs[regTransmitLow]) | int(f.regs[regTransmitHigh])<<8
		raw := f.tx[:length]
		var lrc byte
		for _, b := range raw {
			lrc ^= b
		}
		if lrc != 0 {
			f.t.Errorf("送られたブロックの LRC が合わない")
		}
		block := reverse(raw[:length-1])
		pcb := block[1]
		switch {
		case pcb == 0xc1: // IFS 要求
			f.reply(0xe1, block[3:4])
		case pcb&0x80 == 0: // I ブロック
			apdu := block[3 : 3+int(block[2])]
			f.received = append(f.received, append([]byte{}, apdu...))
			f.pending = f.answer(apdu)
			f.sendPart()
		case pcb&0xc0 == 0x80: // R ブロック: 続きを送る
			f.sendPart()
		}
	}
}

func newTestReader(f *fakeASIE) *Reader {
	r := NewReader(f, 0x4a)
	r.Sleep = func(time.Duration) {}
	return r
}

func TestResetAndTransmit(t *testing.T) {
	f := newFakeASIE(t)
	f.answer = func(apdu []byte) []byte { return append(append([]byte{}, apdu...), 0x90, 0x00) }
	r := newTestReader(f)
	if err := r.Reset(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r.ATR, f.atr) || r.Revision != 3 {
		t.Fatalf("ATR % x、版 %d", r.ATR, r.Revision)
	}
	for i := range 3 {
		apdu := []byte{0x90, 0x30, 0x00, 0x00, byte(i)}
		got, err := r.Transmit(apdu)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, append(apdu, 0x90, 0x00)) {
			t.Fatalf("答え % x", got)
		}
	}
	if r.sequence != 1 { // 3 回送ったので N(S) は 0,1,0 と進んで次は 1
		t.Fatalf("次の番号 %d", r.sequence)
	}
}

func TestChainedResponse(t *testing.T) {
	f := newFakeASIE(t)
	long := make([]byte, 300)
	for i := range long {
		long[i] = byte(i)
	}
	f.answer = func([]byte) []byte { return long }
	r := newTestReader(f)
	if err := r.Reset(); err != nil {
		t.Fatal(err)
	}
	got, err := r.Transmit([]byte{0x90, 0x34, 0x00, 0x00, 0x00})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, long) {
		t.Fatalf("%d バイト受け取った", len(got))
	}
}

func TestNoCard(t *testing.T) {
	f := newFakeASIE(t)
	f.noCard = true
	if err := newTestReader(f).Reset(); err == nil {
		t.Fatal("カードが無いのに通った")
	}
}
