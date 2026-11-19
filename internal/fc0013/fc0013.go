// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// Package fc0013 は Fitipower FC0013（MonsterTV U3S の地上波のチューナー、Zero-IF、水晶 28.8 MHz）を動かす。
// recfriio の Fc0013.cpp（emOEM64.sys の 0x32c78・0x330e8・0x338de・0x33c00 に合わせたもの）から移した。
package fc0013

import (
	"fmt"
	"time"

	"github.com/waya0125/recut100b/internal/i2c"
)

// DefaultAddr は MonsterTV U3S での I2C のアドレス。
const DefaultAddr = 0xc6

const (
	xtalKHz      = 28800
	uhfHighKHz   = 862000  // これ以下なら 0x14 の帯域を UHF にする
	vcoLimitKHz  = 3560000 // VCO の上限
	vco4AltKHz   = 3800000 // 4 倍のときの境目
	initSettle   = 50 * time.Millisecond
	DefaultBWMHz = 6
)

// Tuner は FC0013。
type Tuner struct {
	bus  i2c.Bus
	addr uint8
}

// New は bus（TC90522 の中継）の addr にある FC0013 を作る。
func New(bus i2c.Bus, addr uint8) *Tuner { return &Tuner{bus: bus, addr: addr} }

func (t *Tuner) writeReg(reg, val uint8) error {
	if err := t.bus.Write(t.addr, []byte{reg, val}, true); err != nil {
		return fmt.Errorf("FC0013 のレジスタ %#02x: %w", reg, err)
	}
	return nil
}

// readReg は番地を書き、読みのアドレスを停止なしで送ってから 1 バイト読む（recfriio と同じ順）。
func (t *Tuner) readReg(reg uint8) (uint8, error) {
	if err := t.bus.Write(t.addr, []byte{reg}, true); err != nil {
		return 0, fmt.Errorf("FC0013 のレジスタ %#02x: %w", reg, err)
	}
	if err := t.bus.Write(t.addr|0x01, nil, false); err != nil {
		return 0, fmt.Errorf("FC0013 のレジスタ %#02x: %w", reg, err)
	}
	var b [1]byte
	if err := t.bus.Read(t.addr, b[:], true); err != nil {
		return 0, fmt.Errorf("FC0013 のレジスタ %#02x: %w", reg, err)
	}
	return b[0], nil
}

func (t *Tuner) updateBits(reg, mask, val uint8) error {
	cur, err := t.readReg(reg)
	if err != nil {
		return err
	}
	return t.writeReg(reg, cur&^mask|val&mask)
}

func (t *Tuner) writeTable(table []i2c.RegVal) error {
	for _, rv := range table {
		if err := t.writeReg(rv.Reg, rv.Val); err != nil {
			return err
		}
	}
	return nil
}

// Init は初期化する（emOEM64.sys 0x32c78）。
func (t *Tuner) Init() error {
	err := t.writeTable([]i2c.RegVal{
		{Reg: 0x01, Val: 0x09}, {Reg: 0x02, Val: 0x16}, {Reg: 0x03, Val: 0x00}, {Reg: 0x04, Val: 0x00},
		{Reg: 0x05, Val: 0x17}, {Reg: 0x06, Val: 0x02}, {Reg: 0x07, Val: 0x2a}, {Reg: 0x08, Val: 0xff},
		{Reg: 0x09, Val: 0x6f}, {Reg: 0x0a, Val: 0xb8}, {Reg: 0x0b, Val: 0x82}, {Reg: 0x0c, Val: 0xfe},
		{Reg: 0x0d, Val: 0x01}, {Reg: 0x0e, Val: 0x00}, {Reg: 0x0f, Val: 0x00}, {Reg: 0x10, Val: 0x00},
		{Reg: 0x11, Val: 0x00}, {Reg: 0x12, Val: 0x00}, {Reg: 0x13, Val: 0x00}, {Reg: 0x14, Val: 0x50},
		{Reg: 0x15, Val: 0x01},
	})
	if err != nil {
		return err
	}
	time.Sleep(initSettle)
	return nil
}

// pll は PLL の分周の値。
type pll struct {
	am, pm uint8
	k      uint16
}

func calcPLL(vcoKHz, refKHz uint32) pll {
	xin := (vcoKHz + refKHz/2) / refKHz
	pm := xin >> 3
	am := xin - pm<<3
	if am < 2 {
		am += 8
		pm--
	}
	rem := vcoKHz - vcoKHz/refKHz*refKHz
	k := (rem << 15) / refKHz
	if k >= 0x4000 {
		k = (k + 0x8000) & 0xffff
	}
	return pll{am: uint8(am), pm: uint8(pm), k: uint16(k)}
}

// vcoDivs は周波数 × 倍率が VCO の上限に収まる、いちばん大きい倍率を選ぶための表。
var vcoDivs = []struct{ multi, r05, r06 uint8 }{
	{96, 0x82, 0x00}, {64, 0x02, 0x02}, {48, 0x42, 0x00},
	{32, 0x82, 0x02}, {24, 0x22, 0x00}, {16, 0x42, 0x02},
	{12, 0x12, 0x00}, {8, 0x22, 0x02}, {6, 0x0a, 0x00},
}

// Tune は freqKHz（チャンネルの中心。Zero-IF なのでずらさない）に合わせる。bwMHz は 6・7・8（emOEM64.sys 0x330e8・0x338de）。
func (t *Tuner) Tune(freqKHz uint32, bwMHz uint8) error {
	band := uint8(0x20)
	if freqKHz <= uhfHighKHz {
		band = 0x40
	}
	if err := t.updateBits(0x1d, 0x1c, 0x1c); err != nil {
		return err
	}
	if err := t.updateBits(0x07, 0x10, 0x00); err != nil {
		return err
	}
	if err := t.updateBits(0x14, 0xe0, band); err != nil {
		return err
	}

	f := freqKHz
	multi, r05, r06 := uint8(4), uint8(0x0a), uint8(0x02)
	found := false
	for _, d := range vcoDivs {
		if f*uint32(d.multi) < vcoLimitKHz {
			multi, r05, r06 = d.multi, d.r05, d.r06
			found = true
			break
		}
	}
	if !found {
		multi = 4
		if f*4 < vco4AltKHz {
			r05 = 0x12
		} else {
			f >>= 1
			r05 = 0x0a
		}
		r06 = 0x02
	}

	r06 |= 0x08
	r09 := uint8(0x4f)
	vco := f * uint32(multi)
	p := calcPLL(vco, xtalKHz)
	if p.pm < 11 {
		r09 = 0x6f
		p = calcPLL(vco, xtalKHz/2)
	}

	switch bwMHz {
	case 6:
		r06 |= 0x80
	case 7:
		r06 = r06&0x3f | 0x40
	default:
		r06 &= 0x3f
	}
	r05 |= 0x07

	if err := t.writeTable([]i2c.RegVal{
		{Reg: 0x01, Val: p.am},
		{Reg: 0x02, Val: p.pm},
		{Reg: 0x03, Val: uint8(p.k >> 8)},
		{Reg: 0x04, Val: uint8(p.k)},
		{Reg: 0x05, Val: r05},
		{Reg: 0x06, Val: r06},
		{Reg: 0x09, Val: r09},
	}); err != nil {
		return err
	}

	r11 := uint8(0x00)
	if multi == 64 {
		r11 = 0x04
	}
	if err := t.updateBits(0x11, 0x04, r11); err != nil {
		return err
	}
	// VCO の較正（0x0e を 0x80 → 0x00）。結果が上に張り付いていれば 0x06 の bit 3 を落としてやり直す
	if err := t.writeTable([]i2c.RegVal{{Reg: 0x0e, Val: 0x80}, {Reg: 0x0e, Val: 0x00}, {Reg: 0x0e, Val: 0x00}}); err != nil {
		return err
	}
	vcoCal, err := t.readReg(0x0e)
	if err != nil {
		return err
	}
	if vcoCal&0x3f > 0x3c {
		r06 &= 0xf7
		if err := t.writeTable([]i2c.RegVal{{Reg: 0x06, Val: r06}, {Reg: 0x0e, Val: 0x80}, {Reg: 0x0e, Val: 0x00}}); err != nil {
			return err
		}
	}
	return t.writeTable([]i2c.RegVal{{Reg: 0x08, Val: 0x1f}, {Reg: 0x0c, Val: 0xf8}, {Reg: 0x14, Val: 0x50}})
}

var (
	lnaGain = [8]int{10, 8, 4, 6, 0, 0, 0, 2}
	pgaGain = [4]int{0, 2, 6, 4}
)

// AdjustLNA は受かっている強さ（0x12・0x13）を見て、LNA の段（0x14 の下位 5 ビット）を上げ下げする（emOEM64.sys 0x33c00）。
func (t *Tuner) AdjustLNA() error {
	r14, err := t.readReg(0x14)
	if err != nil {
		return err
	}
	if err := t.writeTable([]i2c.RegVal{{Reg: 0x12, Val: 0x00}, {Reg: 0x13, Val: 0x00}}); err != nil {
		return err
	}
	r12, err := t.readReg(0x12)
	if err != nil {
		return err
	}
	r13, err := t.readReg(0x13)
	if err != nil {
		return err
	}
	total := lnaGain[r13>>5&0x07] + pgaGain[r12>>2&0x03] + pgaGain[r12&0x03] + int(r13&0x1f)*2

	mode := r14 & 0x1f
	next := r14
	switch {
	case mode == 0x00:
		if total > 0x1e {
			next = r14&0xe0 | 0x08
		}
	case mode == 0x08:
		if total < 0x06 {
			next = r14 & 0xe0
		} else if total > 0x32 {
			next = r14&0xf0 | 0x10
		}
	case mode == 0x10:
		if total < 0x1c {
			next = r14&0xe0 | 0x08
		}
	default:
		next = r14&0xf0 | 0x10
	}
	if next != r14 {
		return t.writeReg(0x14, next)
	}
	return nil
}
