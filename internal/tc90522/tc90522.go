// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// Package tc90522 は東芝 TC90522 の復調部を 1 つ（1 アドレス）動かす。
// MonsterTV U3S は 0x20（地上波）と 0x22（衛星）の 2 つを持つ。
// recfriio の Tc90522.cpp（emOEM64.sys の 0x6a3dc・0xfbd50・0xfc350 に合わせたもの）から移した。
package tc90522

import (
	"fmt"
	"time"

	"github.com/waya0125/recut100b/internal/i2c"
)

// 地上波・衛星の共通
const regRepeater = 0xfe // この番地に書くと、続くバイトを裏のチューナーへ中継する

// 地上波（ISDB-T）側のレジスタと値
const (
	terReset       = 0x01
	terResetSoft   = 0x40 // emOEM64.sys 0xfbd50 の表にあるリセット
	terClock0E     = 0x0e
	terClock0F     = 0x0f
	terClockActive = 0x01 // 地上波を起こす
	terClockSleep  = 0x77 // 地上波を寝かせる
	terTSPin1D     = 0x1d
	terTSPin1F     = 0x1f
	terTSEnable    = 0x00
	terTSTristate  = 0xa8
	terAGC23       = 0x23
	terAGC25       = 0x25
	terStatus      = 0x80
	terLockMask    = 0x28 // bit 5 と bit 3 が 0 ならロック
	terCN          = 0x8b // 0x8b..0x8d
)

// 衛星（ISDB-S）側のレジスタと値
const (
	satReset          = 0x03
	satResetSoft      = 0x01
	satTSPin1C        = 0x1c
	satTSPin1F        = 0x1f
	satTSEnable1C     = 0x05
	satTSEnable1F     = 0x00
	satTSTristate1C   = 0x80
	satTSTristate1F   = 0x22
	satTSIDHigh       = 0x8f
	satTSIDLow        = 0x90
	satCN             = 0xbc // 0xbc..0xbd
	satTMCCStatus     = 0xc3
	satTMCCUnlock     = 0x10
	satSlotStatus     = 0xc5
	satSlotLockMask   = 0x03
	satTMCCTSIDs      = 0xce // 0xce から 2 バイトずつ 12 個
	satTMCCTSIDsCount = 12
)

// Demod は TC90522 の復調部 1 つ。
type Demod struct {
	bus  i2c.Bus
	addr uint8
}

// New は bus の addr にある復調部を作る。
func New(bus i2c.Bus, addr uint8) *Demod { return &Demod{bus: bus, addr: addr} }

// Addr は I2C のアドレス。
func (d *Demod) Addr() uint8 { return d.addr }

// WriteReg はレジスタを 1 つ書く。
func (d *Demod) WriteReg(reg, val uint8) error {
	if err := d.bus.Write(d.addr, []byte{reg, val}, true); err != nil {
		return fmt.Errorf("TC90522 %#02x のレジスタ %#02x: %w", d.addr, reg, err)
	}
	return nil
}

// ReadRegs は reg から len(buf) バイト読む（番地を書いて、停止せずに読む）。
func (d *Demod) ReadRegs(reg uint8, buf []byte) error {
	if err := d.bus.Write(d.addr, []byte{reg}, false); err != nil {
		return fmt.Errorf("TC90522 %#02x のレジスタ %#02x: %w", d.addr, reg, err)
	}
	if err := d.bus.Read(d.addr, buf, true); err != nil {
		return fmt.Errorf("TC90522 %#02x のレジスタ %#02x: %w", d.addr, reg, err)
	}
	return nil
}

// ReadReg はレジスタを 1 つ読む。
func (d *Demod) ReadReg(reg uint8) (uint8, error) {
	var b [1]byte
	err := d.ReadRegs(reg, b[:])
	return b[0], err
}

// UpdateBits は reg を読み、mask の所だけ val にして書き戻す。
func (d *Demod) UpdateBits(reg, mask, val uint8) error {
	cur, err := d.ReadReg(reg)
	if err != nil {
		return err
	}
	return d.WriteReg(reg, cur&^mask|val&mask)
}

// WriteTable は表の順に書く。
func (d *Demod) WriteTable(table []i2c.RegVal) error {
	for _, rv := range table {
		if err := d.WriteReg(rv.Reg, rv.Val); err != nil {
			return err
		}
	}
	return nil
}

// InitTer は地上波の側を初期化する（emOEM64.sys 0x6a3dc・0xfbd50。TS のピンはいったん浮かせる）。
func (d *Demod) InitTer() error {
	return d.WriteTable([]i2c.RegVal{
		{Reg: terClock0E, Val: terClockActive}, {Reg: terClock0F, Val: 0x77},
		{Reg: terReset, Val: terResetSoft}, {Reg: 0x20, Val: 0x00},
		{Reg: 0x21, Val: 0xff}, {Reg: 0x22, Val: 0x8f},
		{Reg: terAGC23, Val: 0xfc}, {Reg: 0x24, Val: 0x4c},
		{Reg: terAGC25, Val: 0x80}, {Reg: 0xf4, Val: 0x80},
		{Reg: terTSPin1D, Val: terTSTristate},
	})
}

// InitSat は衛星の側を初期化する（emOEM64.sys 0x6a3dc・0xfc350。TS のピンはいったん浮かせる）。
func (d *Demod) InitSat() error {
	return d.WriteTable([]i2c.RegVal{
		{Reg: 0x07, Val: 0x01}, {Reg: 0x08, Val: 0x10}, {Reg: 0x15, Val: 0x00}, {Reg: 0x1d, Val: 0x00},
		{Reg: 0x0a, Val: 0xff}, {Reg: 0x10, Val: 0xb1}, {Reg: 0x11, Val: 0x40}, {Reg: 0x85, Val: 0x7a},
		{Reg: 0x87, Val: 0x04}, {Reg: satTSPin1C, Val: satTSTristate1C},
		{Reg: satTSPin1F, Val: satTSTristate1F},
		{Reg: satReset, Val: satResetSoft},
	})
}

// SetTerActive は地上波の側を起こして TS を出す（true）か、寝かせて TS のピンを浮かせる（false）。
// 2 つの復調部は TS の線を共有するので、使わない側は浮かせておく。
func (d *Demod) SetTerActive(active bool) error {
	if active {
		return d.WriteTable([]i2c.RegVal{
			{Reg: terClock0E, Val: terClockActive}, {Reg: terClock0F, Val: 0x77},
			{Reg: terTSPin1D, Val: terTSEnable}, {Reg: terTSPin1F, Val: terTSEnable},
		})
	}
	return d.WriteTable([]i2c.RegVal{
		{Reg: terClock0E, Val: terClockSleep}, {Reg: terTSPin1D, Val: terTSTristate},
	})
}

// SetSatActive は衛星の側の TS を出す（true）か、TS のピンを浮かせる（false）。
func (d *Demod) SetSatActive(active bool) error {
	if active {
		return d.WriteTable([]i2c.RegVal{
			{Reg: satTSPin1F, Val: satTSEnable1F}, {Reg: satTSPin1C, Val: satTSEnable1C},
		})
	}
	return d.WriteTable([]i2c.RegVal{
		{Reg: satTSPin1C, Val: satTSTristate1C}, {Reg: satTSPin1F, Val: satTSTristate1F},
	})
}

// HoldAGCForTune は地上波の選局の前（true）と後（false）の AGC の扱い（Windows の公式ドライバーと同じ）。
func (d *Demod) HoldAGCForTune(hold bool) error {
	if hold {
		if err := d.WriteReg(terAGC25, 0x00); err != nil {
			return err
		}
		return d.UpdateBits(terAGC23, 0x01, 0x01)
	}
	if err := d.UpdateBits(terAGC23, 0x01, 0x00); err != nil {
		return err
	}
	return d.WriteReg(terAGC25, 0x80)
}

// ResetTer は地上波の側をリセットする。
func (d *Demod) ResetTer() error { return d.WriteReg(terReset, terResetSoft) }

// ResetSat は衛星の側をリセットする。
func (d *Demod) ResetSat() error { return d.WriteReg(satReset, satResetSoft) }

// TerLocked は地上波がロックしているか。
func (d *Demod) TerLocked() (bool, error) {
	v, err := d.ReadReg(terStatus)
	return v&terLockMask == 0, err
}

// SatTMCCLocked は衛星の TMCC がつかめているか。
func (d *Demod) SatTMCCLocked() (bool, error) {
	v, err := d.ReadReg(satTMCCStatus)
	return v&satTMCCUnlock == 0, err
}

// SatSlotLocked は選んだ TS（スロット）がつかめているか。
func (d *Demod) SatSlotLocked() (bool, error) {
	v, err := d.ReadReg(satSlotStatus)
	return v&satSlotLockMask != 0, err
}

// SelectSatTSID は衛星の TS を TSID で選ぶ。tsid が 0 なら、TMCC にある最初の TSID を選ぶ。
// 選んだ TSID を返す（選べなければ 0）。
func (d *Demod) SelectSatTSID(tsid uint16) (uint16, error) {
	var tmcc [satTMCCTSIDsCount * 2]byte
	if err := d.ReadRegs(satTMCCTSIDs, tmcc[:]); err != nil {
		return 0, err
	}
	if tsid == 0 {
		for i := range satTMCCTSIDsCount {
			cand := uint16(tmcc[i*2])<<8 | uint16(tmcc[i*2+1])
			if cand != 0xffff && cand != 0x0000 {
				tsid = cand
				break
			}
		}
	}
	if tsid == 0 {
		return 0, nil
	}
	if err := d.WriteTable([]i2c.RegVal{
		{Reg: satTSIDHigh, Val: uint8(tsid >> 8)}, {Reg: satTSIDLow, Val: uint8(tsid)},
	}); err != nil {
		return 0, err
	}
	return tsid, d.ResetSat()
}

// TerCNR は地上波の C/N（dB）と生の値を返す。
func (d *Demod) TerCNR() (float64, uint32, error) {
	var b [3]byte
	if err := d.ReadRegs(terCN, b[:]); err != nil {
		return 0, 0, err
	}
	raw := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
	return TerCNRFromRaw(raw), raw, nil
}

// SatCNR は衛星の C/N（dB）と生の値を返す。
func (d *Demod) SatCNR() (float64, uint32, error) {
	var b [2]byte
	if err := d.ReadRegs(satCN, b[:]); err != nil {
		return 0, 0, err
	}
	raw := uint32(b[0])<<8 | uint32(b[1])
	return SatCNRFromRaw(raw), raw, nil
}

// Repeater は、この復調部の裏にあるチューナーへの I2C（中継 0xfe を通る）。
func (d *Demod) Repeater() i2c.Bus { return repeater{d} }

type repeater struct{ d *Demod }

// Write は {0xfe, チューナーのアドレス, data...} を復調部に書く。
func (r repeater) Write(addr uint8, data []byte, stop bool) error {
	if len(data) > 253 {
		return fmt.Errorf("TC90522 %#02x の中継: 書き込みが長すぎる（%d バイト）", r.d.addr, len(data))
	}
	msg := make([]byte, 0, len(data)+2)
	msg = append(msg, regRepeater, addr)
	msg = append(msg, data...)
	return r.d.bus.Write(r.d.addr, msg, stop)
}

// Read は復調部から読む（中継の相手は、前の Write で決まっている）。
func (r repeater) Read(_ uint8, data []byte, stop bool) error {
	return r.d.bus.Read(r.d.addr, data, stop)
}

// 待ち方（recfriio と同じ）
const (
	SatLockTries    = 40
	SatLockInterval = 25 * time.Millisecond
	TerLockTries    = 15
	TerLockInterval = 40 * time.Millisecond
)
