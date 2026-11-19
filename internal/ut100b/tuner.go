// SPDX-License-Identifier: BSD-2-Clause AND MIT
//
// このファイルの初期化・選局の手順と値（レジスタの表、GPIO の順と待ち時間、周波数の式）は、
// recfriio の recfriior5/Hdp.cpp から Go に移したもの。
//   - recfriio: Copyright (c) 2008 FUKAUMI Naoki. 2 条項 BSD（LICENSES/BSD-2-Clause-recfriio.txt）
//   - Hdp.cpp（HDP・UT100B の対応）: tslroom（2012）、stz2012（2016）による recfriio への追加
// レジスタの名前、0x1c を読んでから書く直し、C/N の式、受け始めのデータを捨てる処理は
// waya0125（2026、MIT）。
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions
// are met:
// 1. Redistributions of source code must retain the above copyright
//    notice, this list of conditions and the following disclaimer.
// 2. Redistributions in binary form must reproduce the above copyright
//    notice, this list of conditions and the following disclaimer in the
//    documentation and/or other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE AUTHOR ``AS IS'' AND ANY EXPRESS OR
// IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES
// OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED.
// IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY DIRECT, INDIRECT,
// INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT
// NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
// DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
// THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF
// THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

// Package ut100b は QRS-UT100B のチューナー部（復調部・チューナー・ASIE5606 の TS 設定）を動かす。
//
//	ASV5211（USB ブリッジ） ── I2C ─┬─ 0x20: 復調部（TC90522 の地上波側と同じレジスタの並び）
//	                                │         └ 中継レジスタ 0xfe ─ 0xc0: チューナー（型番不明。NM120 ではない）
//	                                └─ 0x4a / 0x5a: ASIE5606（B-CAS カードの IC。USB 転送の TS に DES もかける）
//
// 調べた結果は docs/QRS-UT100B.md。
package ut100b

import (
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/waya0125/recfriio/internal/asv5211"
)

// I2C のアドレス
const (
	I2CDemod    = 0x20 // TC90522 地上波側
	I2CTuner    = 0xc0 // 復調部の中継の先
	I2CCardHDUS = 0x5a // ASIE5606（HDUS の基板での場所。まずここを見る）
	I2CCardHDP  = 0x4a // ASIE5606（UT100B ではこちらで答える）
)

// TC90522 地上波側のレジスタ（coax の demod/tc90522 と Linux の tc90522 に合わせた名前）
const (
	tcAGC1     = 0x01 // AGC の設定
	tc1C       = 0x1c // 制御。選局で bit 4・5 を立てる
	tc1E       = 0x1e // 出力の線の設定と見られる
	tc1F       = 0x1f // 0x1e と組で選局の前後に書く（意味は未確認）
	tcAGC23    = 0x23 // AGC。bit 0 が自動（0x4d）か止める（0x4c）か
	tcAGC25    = 0x25 // AGC の設定
	tcCNDAT    = 0x8b // C/N の生の値（0x8b..0x8d の 24 ビット）
	tcRepeater = 0xfe // ここに書くと、続くバイトがチューナーへ中継される
)

// ASIE5606 のレジスタ（TS の設定に使うもの）
const (
	cardCtrl     = 0x00
	card04       = 0x04 // 意味は未確認（閉じるときに 0）
	cardTSMode   = 0x05 // USB 転送の TS にかける暗号の方式: 0x91 = XOR、0x80 = DES
	cardIdentity = 0x09 // 下位 3 ビットが 6 なら DES の版
	cardTSKey    = 0x10 // 0x10..0x1f。鍵は守られた書き込みでしか変わらないが、普通の書き込みも TS を出すのに要る
	card80       = 0x80 // 意味は未確認（閉じるときに 0）
)

// TS フィルター（チャンネル 0）のレジスタ
const (
	filterControl = 0x40 // bit 0・1 入力、bit 2 モード、bit 3 動かす
	filterXPIDMin = 0x41 // 捨てる PID の範囲（16 ビット）
	filterXPIDMax = 0x43
)

// GPIO（どの線が何かは元のコメントからの推測）
const (
	gpioTunerPower = 0x08 // 1 で切る、0 で入れる
	gpioCardReset  = 0x11 // ASIE5606 のリセット（1→0→1）
	gpioD4         = 0xd4 // 意味は未確認（切るときに 0）
	gpioD5         = 0xd5 // 意味は未確認（入れるときに 0→1）
)

// チューナーのレジスタ（選局で使うもの）
const (
	tunerControl = 0x11 // 0 にしてから周波数を書き、2 で選局を始める（と見られる）
	tuner13      = 0x13 // 意味は未確認（選局のたびに 0x15）
	tunerFreqLo  = 0x14 // 周波数（1/64 MHz 単位）の下位
	tunerFreqHi  = 0x15 // 上位
)

// 復調部の初期値（レジスタと値の組）
var demodInit = [][2]uint8{
	{0x01, 0x00}, {0x02, 0x00}, {0x03, 0x00}, {0x04, 0x00}, {0x05, 0x00}, {0x06, 0x00}, {0x07, 0x00}, {0x08, 0x00},
	{0x0c, 0x00}, {0x11, 0x21}, {0x12, 0x10}, {0x13, 0x03}, {0x14, 0xe0}, {0x15, 0x42}, {0x16, 0x09}, {0x17, 0x20},
	{0x18, 0x29}, {0x19, 0x13}, {0x1a, 0x29}, {0x1b, 0x13}, {0x1c, 0x2a}, {0x1d, 0xaa}, {0x1e, 0xaa}, {0x1f, 0xa8},
	{0x20, 0x00}, {0x21, 0xff}, {0x22, 0x80}, {0x23, 0x4c}, {0x24, 0x4c}, {0x25, 0x80}, {0x26, 0x00}, {0x27, 0x0c},
	{0x28, 0x60}, {0x29, 0x6b}, {0x2a, 0x40}, {0x2b, 0x40}, {0x2c, 0xff}, {0x2d, 0x00}, {0x2e, 0xff}, {0x2f, 0x00},
	{0x30, 0x20}, {0x31, 0x0f}, {0x32, 0x84}, {0x34, 0x0f}, {0x38, 0x00}, {0x39, 0x0f}, {0x3a, 0x20}, {0x3b, 0x21},
	{0x3c, 0x3f}, {0x3d, 0x10}, {0x3e, 0x08}, {0x3f, 0x0c}, {0x40, 0x0c}, {0x41, 0x00}, {0x42, 0x00}, {0x43, 0x4f},
	{0x44, 0xff}, {0x46, 0x20}, {0x47, 0x00}, {0x48, 0x90}, {0x49, 0xe6}, {0x4a, 0x02}, {0x4b, 0x54}, {0x4c, 0x00},
	{0x50, 0x04}, {0x51, 0x58}, {0x52, 0x20}, {0x54, 0x57}, {0x55, 0xf1}, {0x56, 0x20}, {0x57, 0x70}, {0x58, 0x60},
	{0x5c, 0x50}, {0x5d, 0x00}, {0x5e, 0x01}, {0x5f, 0x80}, {0x70, 0x18}, {0x71, 0x00}, {0x72, 0x00}, {0x75, 0x22},
	{0x76, 0x00}, {0x77, 0x01}, {0x7c, 0x00}, {0x7d, 0x52}, {0xba, 0x00}, {0xbb, 0x00}, {0xbc, 0x00}, {0xc2, 0x10},
	{0xc7, 0x00}, {0xe4, 0x1a}, {0xe6, 0x2f}, {0xe9, 0x08}, {0xec, 0x00}, {0xef, 0x00},
}

// チューナー（0xc0）の初期化。型番が分からないので、レジスタは番号のまま。
var tunerInit = [][2]uint8{
	{0x2c, 0x44}, {0x4d, 0x40}, {0x7f, 0x02}, {0x9a, 0x52}, {0x48, 0x5a}, {0x76, 0x1a},
	{0x6a, 0x48}, {0x64, 0x28}, {0x66, 0xe6}, {0x35, 0x11}, {0x7e, 0x01}, {0x0b, 0x99},
	{0x0c, 0x00}, {0x10, 0x40}, {0x12, 0xca}, {0x16, 0x90}, {0x32, 0x36}, {0xd8, 0x18},
	{0x05, 0x01},
}

// Tuner は UT100B のチューナー部。
type Tuner struct {
	b *asv5211.Bridge
	// CardAddr は ASIE5606 が答えた I2C のアドレス（Open のあとで決まる）
	CardAddr uint8
	// Sleep は待つ関数（試験で差し替える）
	Sleep func(time.Duration)
	// Log に書く先があれば、経過を書く
	Log  io.Writer
	open bool
}

// NewTuner は b に命令を送るチューナーを作る。
func NewTuner(b *asv5211.Bridge) *Tuner {
	return &Tuner{b: b, Sleep: time.Sleep, Log: io.Discard}
}

func (t *Tuner) wait(ms int) { t.Sleep(time.Duration(ms) * time.Millisecond) }

// errs は続けて送る命令の最初の失敗を覚えておく。
type errs struct{ err error }

func (e *errs) do(err error) {
	if e.err == nil && err != nil {
		e.err = err
	}
}

func (t *Tuner) regWrite(e *errs, addr, reg, value uint8) {
	if e.err != nil {
		return
	}
	_, err := t.b.RegWrite(addr, reg, value)
	e.do(err)
}

func (t *Tuner) regRead(e *errs, addr, reg uint8) uint8 {
	if e.err != nil {
		return 0
	}
	v, _, err := t.b.RegRead(addr, reg)
	e.do(err)
	return v
}

func (t *Tuner) gpio(e *errs, mask, value uint8) {
	if e.err == nil {
		e.do(t.b.GPIO(mask, value))
	}
}

// tunerSend は復調部の中継（0xfe）を通して、チューナー（0xc0）へバイト列を送る。
func (t *Tuner) tunerSend(e *errs, bytes ...uint8) {
	if e.err == nil {
		e.do(t.b.I2CWrite(I2CDemod, append([]uint8{tcRepeater, I2CTuner}, bytes...)))
	}
}

// Open はチューナーの電源を入れ、復調部・チューナー・ASIE5606 を初期化する（recfriio の UsbProcBegin）。
func (t *Tuner) Open() error {
	var e errs
	fmt.Fprint(t.Log, "opening UT100B...")
	tsMode := uint8(0x91)
	t.CardAddr = I2CCardHDUS

	_, err := t.b.CustomerInfo()
	e.do(err)
	if e.err == nil {
		_, err = t.b.SysCtrlRead(0x0002)
		e.do(err)
	}
	t.gpio(&e, 0xfb, 0x07)
	if e.err == nil {
		e.do(t.b.GPIOEx(0x02, 0x00))
	}

	// 電源を切る
	t.gpio(&e, gpioD4, 0x00)
	t.gpio(&e, gpioTunerPower, gpioTunerPower)
	t.wait(50)
	if e.err == nil {
		_, err = t.b.SysCtrlRead(0x0002)
		e.do(err)
	}

	// チューナーの電源を入れる
	t.gpio(&e, gpioTunerPower, gpioTunerPower)
	t.wait(50)
	t.gpio(&e, gpioTunerPower, 0x00)
	t.wait(150)
	t.gpio(&e, gpioCardReset, 0xff)
	t.wait(50)
	t.gpio(&e, gpioD5, 0x00)
	t.wait(50)
	t.gpio(&e, gpioD5, 0xff)
	t.wait(100)

	// チューナーと復調部の初期化
	t.tunerSend(&e, 0xff) // 1 バイトだけ 0xff を送る（リセットと見られる）
	for _, rv := range demodInit {
		t.regWrite(&e, I2CDemod, rv[0], rv[1])
	}
	t.regWrite(&e, I2CDemod, tc1C, t.regRead(&e, I2CDemod, tc1C)&0xeb|0x28)
	for _, rv := range tunerInit {
		t.tunerSend(&e, rv[0], rv[1])
	}

	// ASIE5606 の電源・リセット
	t.gpio(&e, gpioCardReset, 0xff)
	t.wait(50)
	t.gpio(&e, gpioCardReset, 0x00)
	t.wait(50)
	t.gpio(&e, gpioCardReset, 0xff)
	t.wait(100)

	// ASIE5606 がどちらのアドレスで答えるか、どの版かを見る
	if e.err == nil {
		identity, ok, err := t.b.RegRead(t.CardAddr, cardIdentity)
		e.do(err)
		if e.err == nil && !ok {
			t.CardAddr = I2CCardHDP
			identity, _, err = t.b.RegRead(t.CardAddr, cardIdentity)
			e.do(err)
		}
		if identity&7 == 6 {
			tsMode = 0x80 // DES
		}
	}
	if e.err == nil {
		_, err = t.b.HighSpeed()
		e.do(err)
	}
	t.wait(30)

	// USB 転送の TS にかける暗号の方式と、0x10..0x1f への 0 の書き込み。
	// この普通の書き込みでは鍵は変わらないが、無いと TS が出てこない（docs/QRS-UT100B.md）
	t.regWrite(&e, t.CardAddr, cardTSMode, tsMode)
	for reg := uint8(cardTSKey); reg < cardTSKey+0x10; reg++ {
		t.regWrite(&e, t.CardAddr, reg, 0x00)
	}
	if e.err != nil {
		fmt.Fprintln(t.Log, "failed.")
		return e.err
	}
	if tsMode != 0x80 {
		fmt.Fprintln(t.Log, "failed.")
		return errors.New("ASIE5606 が DES の版ではない（この版の USB 転送の暗号は扱えない）")
	}
	t.open = true
	fmt.Fprintln(t.Log, "done.")
	return nil
}

// Frequency はチャンネルの中心の周波数（MHz）を返す。catv が真なら CATV の C13..C63。
func Frequency(channel int, catv bool) (int, error) {
	if !catv {
		if channel < 13 || channel > 62 {
			return 0, fmt.Errorf("UHF のチャンネル %d は 13..62 の外", channel)
		}
		return 473 + (channel-13)*6, nil
	}
	switch {
	case channel < 13 || channel > 63:
		return 0, fmt.Errorf("CATV のチャンネル %d は 13..63 の外", channel)
	case channel <= 22:
		return 111 + (channel-13)*6, nil
	case channel == 23:
		return 225, nil
	case channel <= 27:
		return 233 + (channel-24)*6, nil
	default:
		return 225 + (channel-23)*6, nil
	}
}

// Tune は周波数 freqMHz を選び、TS を流し始める（recfriio の setChannel）。
func (t *Tuner) Tune(freqMHz int) error {
	if !t.open {
		return errors.New("チューナーを開いていない")
	}
	var e errs
	fmt.Fprintf(t.Log, "freq = %d\n", freqMHz)
	code := uint16(freqMHz * 64)            // 1/64 MHz 単位
	const xpidMin, xpidMax = 0x1fff, 0x1fff // ヌルパケットだけ捨てる

	// 選局の前の復調部の設定
	t.regWrite(&e, I2CDemod, tc1C, t.regRead(&e, I2CDemod, tc1C)&0xfb|0x38)
	t.regWrite(&e, I2CDemod, tcAGC25, 0x00)
	t.regWrite(&e, I2CDemod, tcAGC23, 0x4d)
	t.regWrite(&e, I2CDemod, tc1E, 0xaa)
	t.regWrite(&e, I2CDemod, tc1F, 0xa8)

	// チューナーに周波数を書く
	t.tunerSend(&e, tunerControl, 0x00)
	t.tunerSend(&e, tuner13, 0x15)
	t.tunerSend(&e, tunerFreqLo, uint8(code))
	t.tunerSend(&e, tunerFreqHi, uint8(code>>8))
	t.tunerSend(&e, tunerControl, 0x02)

	// 選局の後の復調部の設定
	t.regWrite(&e, I2CDemod, tc1E, 0xa2)
	t.regWrite(&e, I2CDemod, tc1F, 0x08)
	t.regWrite(&e, I2CDemod, tcAGC1, 0x40)
	t.regWrite(&e, I2CDemod, tcAGC23, 0x4c)

	// TS フィルター（チャンネル 0）: リセットし、入力・動作を有効にして、捨てる PID を決める
	if e.err == nil {
		e.do(t.b.FilterReset(0, 1))
	}
	if e.err == nil {
		v, err := t.b.FilterRead(filterControl)
		e.do(err)
		if e.err == nil {
			e.do(t.b.FilterWrite8(filterControl, v|0x0f))
		}
	}
	if e.err == nil {
		e.do(t.b.FilterWrite16(filterXPIDMin, xpidMin))
	}
	if e.err == nil {
		e.do(t.b.FilterWrite16(filterXPIDMax, xpidMax))
	}

	// 受信を始める（チャンネル 0）
	if e.err == nil {
		e.do(t.b.StreamStart(0))
	}
	t.wait(150)
	return e.err
}

// CNR は復調部の C/N（dB）を読む。つかめていなければ 0。raw は生の値。
// 式は Linux の tc90522 ドライバーの地上波のもの。
func (t *Tuner) CNR() (cnr float64, raw uint32, err error) {
	var e errs
	raw = uint32(t.regRead(&e, I2CDemod, tcCNDAT)) << 16
	raw |= uint32(t.regRead(&e, I2CDemod, tcCNDAT+1)) << 8
	raw |= uint32(t.regRead(&e, I2CDemod, tcCNDAT+2))
	if e.err != nil {
		return 0, 0, e.err
	}
	return CNRFromRaw(raw), raw, nil
}

// CNRFromRaw は C/N の生の値を dB にする。
func CNRFromRaw(raw uint32) float64 {
	if raw == 0 {
		return 0
	}
	p := 10 * math.Log10(5505024/float64(raw))
	cnr := 0.000024*p*p*p*p - 0.0016*p*p*p + 0.0398*p*p + 0.5491*p + 3.0965
	if cnr < 0 {
		cnr = 0
	}
	return cnr
}

// Close は受信を止め、電源を切る（recfriio の UsbProcEnd）。
func (t *Tuner) Close() error {
	if !t.open {
		return nil
	}
	t.open = false
	var e errs
	fmt.Fprint(t.Log, "closing UT100B...")
	t.wait(100)
	e.do(t.b.StreamStop(0))
	if e.err == nil {
		e.do(t.b.FilterReset(0, 1))
	}
	t.regWrite(&e, t.CardAddr, card80, 0x00)
	t.regWrite(&e, t.CardAddr, card04, 0x00)
	t.regWrite(&e, t.CardAddr, cardCtrl, 0x00)

	// 電源を切る
	t.wait(50)
	t.gpio(&e, gpioD4, 0x00)
	t.gpio(&e, gpioTunerPower, gpioTunerPower)
	t.wait(50)
	if e.err != nil {
		fmt.Fprintln(t.Log, "failed.")
		return e.err
	}
	fmt.Fprintln(t.Log, "done.")
	return nil
}
