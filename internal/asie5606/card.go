// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package asie5606

// ASIE5606 の内蔵 B-CAS スロットを、ASV5211 の I2C 経由で T=1 で使う（recfriio の AsieCardReader を Go にしたもの）。
//
// ASIE5606 は CCID のリーダーではなく、カードの線へのバイトの通り道を I2C のレジスタとして見せるだけなので、
// T=1 のブロックの組み立て（NAD・PCB・LEN・LRC、続きの受け取り）はここで行う。
// カードの線のビットの並びは逆なので、送るブロックも受けるブロックも 1 バイトずつビットを逆にする。
// LRC は逆にした後のバイトで計算する。
//
// 手順（レジスタの番号、命令、待ち時間）は coax（github.com/Till0196/coax、Apache-2.0 OR GPL-2.0-or-later）の
// internal/coaxd/smartcard/asie5606 で調べられたものに従った（コードは書き下ろし）。

import (
	"errors"
	"fmt"
	"io"
	"math/bits"
	"time"
)

// レジスタ
const (
	regCommand      = 0x00
	regProtocol     = 0x01
	regTransmitLow  = 0x02
	regReceiveLow   = 0x03
	regStatus       = 0x04 // bit 0: カードあり（読む）、bit 7: カードの電源（書く）
	regVersion      = 0x09
	regTransmitHigh = 0x38
	regReceiveHigh  = 0x39
	regWindowPage   = 0x3a
	regWindow       = 0x40 // 0x40..0x7f の 64 バイトの窓
	windowSize      = 0x40
)

// regCommand に書く命令
const (
	cmdIdle        = 0x00
	cmdPrepare     = 0x80
	cmdPower       = 0x08
	cmdReset       = 0x09
	cmdTransmit    = 0x0a
	cmdAcknowledge = 0x0c
)

const (
	statusCardPresent = 0x01
	statusPowerOn     = 0x80
)

// 待ち時間（ms）。カードのいちばん遅い答えには、先頭まで 42 回・残りに 8 回の 50ms の見回りが要る
const (
	pollMS     = 50
	headPolls  = 42
	tailPolls  = 8
	parkMS     = 10
	prepareMS  = 100
	resetMS    = 200
	transmitMS = 100 // ブロックを線に送り出す時間。これより前に見ると前の答えの長さが見える
)

const maxInformation = 254

// Registers は ASIE5606 のレジスタを読み書きする口（ASV5211 の 0x02・0x03）。
type Registers interface {
	RegRead(addr, reg uint8) (value uint8, ok bool, err error)
	RegWrite(addr, reg, value uint8) (ok bool, err error)
}

// Reader は内蔵の B-CAS スロット。bcas.Transport として使える。
type Reader struct {
	r        Registers
	address  uint8
	extended bool  // ページ切り替えと長さの上位バイトのレジスタがある版か
	sequence uint8 // T=1 の次に送る I ブロックの番号 N(S)
	// Sleep は待つ関数（試験で差し替える）
	Sleep func(time.Duration)
	// Log に経過を書く
	Log io.Writer
	// Revision は ASIE5606 の版（Reset のあとで決まる）
	Revision int
	// ATR は最後に受け取った ATR
	ATR []byte
}

// NewReader は I2C の address にいる ASIE5606 のスロットを使う Reader を作る。
func NewReader(r Registers, address uint8) *Reader {
	return &Reader{r: r, address: address, extended: true, Sleep: time.Sleep, Log: io.Discard}
}

func (c *Reader) wait(ms int) { c.Sleep(time.Duration(ms) * time.Millisecond) }

func (c *Reader) read(reg uint8) (uint8, error) {
	v, ok, err := c.r.RegRead(c.address, reg)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("ASIE5606 のレジスタ %#02x を読めない", reg)
	}
	return v, nil
}

func (c *Reader) write(reg, value uint8) error {
	ok, err := c.r.RegWrite(c.address, reg, value)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("ASIE5606 のレジスタ %#02x に書けない", reg)
	}
	return nil
}

func (c *Reader) command(op uint8) error { return c.write(regCommand, op) }

// frame はブロックをカードのビットの並びにして、LRC を付ける。
func frame(block []byte) []byte {
	out := make([]byte, 0, len(block)+1)
	var lrc byte
	for _, b := range block {
		r := bits.Reverse8(b)
		out = append(out, r)
		lrc ^= r
	}
	return append(out, lrc)
}

var errCardRemoved = errors.New("B-CAS カードが抜かれた")

// available は受け取りの入れ物にたまったバイト数。
func (c *Reader) available() (int, error) {
	var high uint8
	var err error
	if c.extended {
		if high, err = c.read(regReceiveHigh); err != nil {
			return 0, err
		}
	}
	low, err := c.read(regReceiveLow)
	if err != nil {
		return 0, err
	}
	status, err := c.read(regStatus)
	if err != nil {
		return 0, err
	}
	if status&statusCardPresent == 0 {
		return 0, errCardRemoved
	}
	return int(low) | int(high&1)<<8, nil
}

func (c *Reader) waitFor(minimum, polls int) (int, error) {
	for range polls {
		c.wait(pollMS)
		n, err := c.available()
		if err != nil {
			return 0, err
		}
		if n >= minimum {
			return n, nil
		}
	}
	return 0, fmt.Errorf("B-CAS カードから %d バイトの答えが来ない", minimum)
}

func (c *Reader) readWindow(start, size int, out []byte) ([]byte, error) {
	for offset := start; offset < start+size; offset++ {
		page, within := offset/windowSize, offset%windowSize
		if c.extended && within == 0 {
			if err := c.write(regWindowPage, uint8(page)); err != nil {
				return nil, err
			}
		}
		v, err := c.read(regWindow + uint8(within))
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (c *Reader) writeWindow(data []byte) error {
	for offset, b := range data {
		page, within := offset/windowSize, offset%windowSize
		if c.extended && within == 0 {
			if err := c.write(regWindowPage, uint8(page)); err != nil {
				return err
			}
		}
		if err := c.write(regWindow+uint8(within), b); err != nil {
			return err
		}
	}
	return nil
}

func (c *Reader) send(block []byte) error {
	if err := c.write(regTransmitLow, uint8(len(block))); err != nil {
		return err
	}
	if c.extended {
		if err := c.write(regTransmitHigh, uint8(len(block)>>8)); err != nil {
			return err
		}
	}
	if err := c.writeWindow(block); err != nil {
		return err
	}
	if err := c.command(cmdTransmit); err != nil {
		return err
	}
	c.wait(transmitMS)
	return nil
}

// receive はブロックを受け取る（カードのビットの並び・LRC 付きのまま）。ATR は 13 バイトを一度に、
// それ以外は 3 バイト目の長さを見てから残りを受け取る。
func (c *Reader) receive(atr bool) ([]byte, error) {
	minimum := 3
	if atr {
		minimum = 13
	}
	count, err := c.waitFor(minimum, headPolls)
	if err != nil {
		return nil, err
	}
	block, err := c.readWindow(0, count, nil)
	if err != nil {
		return nil, err
	}
	if !atr {
		total := int(bits.Reverse8(block[2])) + 4 // ヘッダー 3 + 情報 + LRC 1
		if total > count {
			second, err := c.waitFor(total, tailPolls)
			if err != nil {
				return nil, err
			}
			if block, err = c.readWindow(count, second-count, block); err != nil {
				return nil, err
			}
		}
	}
	return block, c.command(cmdAcknowledge)
}

// Reset はカードの電源を入れて ATR を受け取り、T=1 のやり取りを始める。
func (c *Reader) Reset() error {
	version, err := c.read(regVersion)
	if err != nil {
		return fmt.Errorf("ASIE5606 が %#02x で答えない: %w", c.address, err)
	}
	c.Revision = int(version&0x3e) >> 1
	c.extended = c.Revision != 0

	// 前に使った人の途中のやり取りが受け取りの入れ物に残らないよう、いったん止める
	if err := c.command(cmdPrepare); err != nil {
		return err
	}
	c.wait(parkMS)
	if err := c.command(cmdIdle); err != nil {
		return err
	}
	if err := c.write(regStatus, 0); err != nil {
		return err
	}
	if err := c.command(cmdIdle); err != nil {
		return err
	}
	c.wait(prepareMS)

	status, err := c.read(regStatus)
	if err != nil {
		return err
	}
	if status&statusCardPresent == 0 {
		return errors.New("内蔵のスロットに B-CAS カードが無い")
	}
	if err := c.command(cmdPower); err != nil {
		return err
	}
	if err := c.write(regStatus, statusPowerOn); err != nil {
		return err
	}
	c.wait(resetMS)
	if err := c.command(cmdReset); err != nil {
		return err
	}
	raw, err := c.receive(true)
	if err != nil {
		return err
	}
	c.ATR = make([]byte, len(raw))
	for i, b := range raw {
		c.ATR[i] = bits.Reverse8(b)
	}
	if err := c.write(regProtocol, 0x01); err != nil { // T=1
		return err
	}
	c.sequence = 0

	// 受け取れる情報の大きさ（IFSD）を最大の 254 にしてもらう。答えは読まずに捨てる
	if c.extended {
		if err := c.send(frame([]byte{0x00, 0xc1, 0x01, 0xfe})); err != nil {
			return err
		}
		if err := c.command(cmdAcknowledge); err != nil {
			return err
		}
	}
	fmt.Fprintf(c.Log, "B-CAS: card ready on the built-in slot (ASIE5606 revision %d)\n", c.Revision)
	return nil
}

// Transmit は APDU を 1 つ送り、答え（SW1 SW2 を含む）を受け取る。
func (c *Reader) Transmit(apdu []byte) ([]byte, error) {
	bufferSize := windowSize
	if c.extended {
		bufferSize = 0x140
	}
	limit := min(bufferSize-4, maxInformation)
	if len(apdu) == 0 || len(apdu) > limit {
		return nil, fmt.Errorf("APDU の長さ %d が 1..%d の外", len(apdu), limit)
	}
	block := frame(append([]byte{0x00, c.sequence << 6, uint8(len(apdu))}, apdu...))
	var response []byte
	// 続きの印を外さないカードで止まらないよう、回数に上限を付ける
	for range bufferSize/4 + 1 {
		if err := c.send(block); err != nil {
			return nil, err
		}
		received, err := c.receive(false)
		if err != nil {
			return nil, err
		}
		if len(received) < 4 {
			return nil, errors.New("B-CAS カードの答えが短い")
		}
		var lrc byte
		for _, b := range received {
			lrc ^= b
		}
		if lrc != 0 {
			return nil, errors.New("B-CAS カードの答えの LRC が合わない")
		}
		for i := range received {
			received[i] = bits.Reverse8(received[i])
		}
		response = append(response, received[3:len(received)-1]...)
		pcb := received[1]
		if pcb&0x20 == 0 { // 続きなし: 次に送る番号を進める
			c.sequence ^= 1
			return response, nil
		}
		// 続きあり: R ブロックで、次に送ってほしいブロックの番号を返す
		next := (pcb>>6)&1 ^ 1
		block = frame([]byte{0x00, 0x80 | next<<4, 0x00})
	}
	return nil, errors.New("B-CAS カードの答えが終わらない")
}
