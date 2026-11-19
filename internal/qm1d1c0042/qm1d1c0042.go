// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// Package qm1d1c0042 はシャープ QM1D1C0042（MonsterTV U3S の衛星のチューナー、水晶 16 MHz）を動かす。
// 書いた値を手元に控え、選局ではそこから組み立てる（recfriio の Qm1d1c0042.cpp、Linux の qm1d1c0042.c と同じ考え方）。
package qm1d1c0042

import (
	"fmt"
	"time"

	"github.com/waya0125/recut100b/internal/i2c"
)

// DefaultAddr は MonsterTV U3S での I2C のアドレス。
const DefaultAddr = 0xc0

const xtalKHz = 16000

var initRegs = [32]uint8{
	0x68, 0x1c, 0xb0, 0x10, 0xbc, 0xc1, 0x11, 0x35,
	0x09, 0x06, 0x00, 0x00, 0xc3, 0x47, 0xb5, 0x30,
	0x00, 0xff, 0xf3, 0x20, 0x3f, 0x25, 0x5c, 0xd6,
	0x55, 0xcf, 0x95, 0xf6, 0x36, 0xf2, 0x09, 0x00,
}

// 帯ごとの設定（0x02 の bit 7 と bit 4..6）
var convTable = [9][3]uint32{
	{2151000, 1, 7}, {1950000, 1, 6}, {1800000, 1, 5},
	{1600000, 1, 4}, {1450000, 1, 3}, {1250000, 1, 2},
	{1200000, 0, 7}, {975000, 0, 6}, {950000, 0, 0},
}

// Tuner は QM1D1C0042。
type Tuner struct {
	bus  i2c.Bus
	addr uint8
	regs [32]uint8
}

// New は bus（TC90522 の中継）の addr にある QM1D1C0042 を作る。
func New(bus i2c.Bus, addr uint8) *Tuner { return &Tuner{bus: bus, addr: addr} }

func (t *Tuner) writeReg(reg, val uint8) error {
	if err := t.bus.Write(t.addr, []byte{reg, val}, true); err != nil {
		return fmt.Errorf("QM1D1C0042 のレジスタ %#02x: %w", reg, err)
	}
	return nil
}

// Init は 0x01..0x1f を既定の値にする。
func (t *Tuner) Init() error {
	t.regs = initRegs
	for r := 1; r < len(t.regs); r++ {
		if err := t.writeReg(uint8(r), t.regs[r]); err != nil {
			return err
		}
	}
	return nil
}

// Tune は freqKHz（第一 IF。BS・CS の中間周波数）に合わせる。
func (t *Tuner) Tune(freqKHz uint32) error {
	t.regs[0x08] = t.regs[0x08]&0xf0 | 0x02
	t.regs[0x13] = t.regs[0x13]&0x9f | 0x20

	val02 := t.regs[0x02] & 0x0f
	for i := range 8 {
		if freqKHz < convTable[i][0] && freqKHz >= convTable[i+1][0] {
			val02 |= uint8(convTable[i][1]<<7 | convTable[i][2]<<4)
			break
		}
	}
	t.regs[0x02] = val02

	a := (freqKHz + xtalKHz/2) / xtalKHz
	t.regs[0x06] = t.regs[0x06]&0x40 | uint8((a-12)/4)
	t.regs[0x07] = t.regs[0x07]&0xf0 | uint8((a-4*((a-12)/4+1)-5)&0x0f)

	b := int64(uint64(freqKHz)<<20/xtalKHz) - int64(a)<<20
	var sd uint32
	if b >= 0 {
		sd = uint32(b)
	} else {
		sd = uint32(1<<22 + b)
	}
	t.regs[0x09] = t.regs[0x09]&0xc0 | uint8(sd>>16&0x3f)
	t.regs[0x0a] = uint8(sd >> 8)
	t.regs[0x0b] = uint8(sd)

	for _, r := range []uint8{0x02, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x13} {
		if err := t.writeReg(r, t.regs[r]); err != nil {
			return err
		}
	}

	if err := t.writeReg(0x0c, t.regs[0x0c]&0x3f); err != nil {
		return err
	}
	time.Sleep(3 * time.Millisecond)
	if err := t.writeReg(0x0c, t.regs[0x0c]|0xc0); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)

	if err := t.writeReg(0x08, 0x09); err != nil {
		return err
	}
	return t.writeReg(0x13, t.regs[0x13])
}
