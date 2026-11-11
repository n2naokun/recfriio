// SPDX-License-Identifier: MIT
// Copyright (c) 2026 KASHIWAYA Rikuto

// Package asv5211 は ASICEN ASV5211（QRS-UT100B の USB ブリッジ）のベンダーリクエストを送る。
//
// リクエストの名前は、PLEX の Linux ドライバーのライブラリ（libPlexLib、関数名が残っている）と
// coax（github.com/Till0196/coax の internal/coaxd/usb/bridge/asv5211）に合わせた。
// 送る中身（wValue・wIndex・wLength の作り方）は recfriio の Hdp.cpp で動いていたものと同じにしてある。
// 調べた結果は docs/QRS-UT100B.md。
package asv5211

import (
	"fmt"
	"io"
	"os"
)

// ベンダーリクエスト
const (
	ReqRegRead      = 0x02 // PLEX: ReadI2CData。I2C の機器のレジスタを 1 つ読む
	ReqRegWrite     = 0x03 // PLEX: WriteI2CData。1 つ書く
	ReqFilterRead   = 0x04 // TS フィルター（チャンネル 0）のレジスタを読む
	ReqFilterWrite  = 0x05 // 書く
	ReqStreamStart  = 0x06 // PLEX: DSC。TS を流し始める
	ReqStreamStop   = 0x07
	ReqGPIO         = 0x08 // wValue = mask << 8 | value
	ReqFilterReset  = 0x09 // PLEX: ResetChannel。wValue = mode << 8 | チャンネル
	ReqHighSpeed    = 0x0a // PLEX: GetDevHighSpeed。USB が高速かを問い合わせるだけ
	ReqCustomerInfo = 0x0c // PLEX: GetCusInfo
	ReqI2CFill      = 0x0d // PLEX: I2C BufFill。I2C で送るバイトを 3 つずつ溜める
	ReqI2CSend      = 0x0e // PLEX: I2C BufSend。溜めたものを送る
	ReqGPIOEx       = 0x10 // 2 つ目の GPIO
	ReqSysCtrlRead  = 0x17 // PLEX: ReadSysCtrlData
)

// requestType はどのリクエストも同じ（受け取り・ベンダー・機器あて）。
const requestType = 0xc0

// Timeout は制御転送の待ち時間（ms）。
const Timeout = 1000

// Transport は制御転送を送る先（usbfs.Device。試験では偽物）。
type Transport interface {
	Control(requestType, request uint8, value, index uint16, data []byte, timeoutMS uint32) (int, error)
}

// Bridge は ASV5211 にリクエストを送る。
type Bridge struct {
	t Transport
	// Trace に書く先があれば、送ったリクエストと答えの先頭 2 バイトを書く
	// （recfriio の RECFRIIO_USB_TRACE と同じ形）。
	Trace io.Writer
}

// New は t に送る Bridge を作る。環境変数 RECUT100B_USB_TRACE があれば標準エラーに記録を書く。
func New(t Transport) *Bridge {
	b := &Bridge{t: t}
	if os.Getenv("RECUT100B_USB_TRACE") != "" {
		b.Trace = os.Stderr
	}
	return b
}

// Send はリクエストを 1 つ送り、答え（length バイト）を返す。
func (b *Bridge) Send(request uint8, value, index uint16, length int) ([]byte, error) {
	data := make([]byte, length)
	n, err := b.t.Control(requestType, request, value, index, data, Timeout)
	if b.Trace != nil {
		var d0, d1 byte
		if length > 0 {
			d0 = data[0]
		}
		if length > 1 {
			d1 = data[1]
		}
		fmt.Fprintf(b.Trace, "usb req=%02x val=%04x idx=%04x len=%d -> %02x %02x\n", request, value, index, length, d0, d1)
	}
	if err != nil {
		return nil, fmt.Errorf("ASV5211 のリクエスト %#02x（val %#04x idx %#04x）: %w", request, value, index, err)
	}
	return data[:n], nil
}

// RegRead は I2C の機器 addr のレジスタ reg を読む。ok は答えの先頭（状態）が 1 だったか。
func (b *Bridge) RegRead(addr, reg uint8) (value uint8, ok bool, err error) {
	data, err := b.Send(ReqRegRead, uint16(reg)<<8|uint16(addr), 0, 2)
	if err != nil {
		return 0, false, err
	}
	if len(data) < 2 {
		return 0, false, fmt.Errorf("ASV5211 のレジスタ読み（%#02x の %#02x）の答えが %d バイト", addr, reg, len(data))
	}
	return data[1], data[0] == 1, nil
}

// RegWrite は I2C の機器 addr のレジスタ reg に value を書く。ok は答えの状態が 1 だったか。
func (b *Bridge) RegWrite(addr, reg, value uint8) (ok bool, err error) {
	data, err := b.Send(ReqRegWrite, uint16(reg)<<8|uint16(addr), uint16(value), 2)
	if err != nil {
		return false, err
	}
	return len(data) > 0 && data[0] == 1, nil
}

// GPIO は 1 つ目の GPIO の mask のビットを value にする。
func (b *Bridge) GPIO(mask, value uint8) error {
	_, err := b.Send(ReqGPIO, uint16(mask)<<8|uint16(value), 0, 1)
	return err
}

// GPIOEx は 2 つ目の GPIO の mask のビットを value にする。
func (b *Bridge) GPIOEx(mask, value uint8) error {
	_, err := b.Send(ReqGPIOEx, uint16(mask)<<8|uint16(value), 0, 1)
	return err
}

// CustomerInfo は状態 1 バイトと 0x39 バイトの基板の情報を読む。
func (b *Bridge) CustomerInfo() ([]byte, error) { return b.Send(ReqCustomerInfo, 0, 0, 0x3a) }

// SysCtrlRead はシステム制御の領域 addr から読む（状態 1 バイト + 2 バイト）。
func (b *Bridge) SysCtrlRead(addr uint16) ([]byte, error) { return b.Send(ReqSysCtrlRead, addr, 0, 3) }

// HighSpeed は USB が高速でつながっているかを問い合わせる。
func (b *Bridge) HighSpeed() ([]byte, error) { return b.Send(ReqHighSpeed, 0, 0, 1) }

// I2CWrite は I2C の機器 addr に bytes を送る。ASV5211 に 3 バイトずつ溜めてから送らせる。
func (b *Bridge) I2CWrite(addr uint8, bytes []byte) error {
	p := bytes
	offset := 0
	for ; len(p) >= 3; p = p[3:] {
		if _, err := b.Send(ReqI2CFill, uint16(p[0])<<8|uint16(offset), uint16(p[2])<<8|uint16(p[1]), 4); err != nil {
			return err
		}
		offset += 3
	}
	switch len(p) {
	case 2:
		if _, err := b.Send(ReqI2CFill, uint16(p[0])<<8|uint16(offset), uint16(p[1]), 3); err != nil {
			return err
		}
		offset += 2
	case 1:
		if _, err := b.Send(ReqI2CFill, uint16(p[0])<<8|uint16(offset), 0, 2); err != nil {
			return err
		}
		offset++
	}
	_, err := b.Send(ReqI2CSend, uint16(addr), 0, offset+1)
	return err
}

// FilterReset は TS フィルターのチャンネル channel を mode でリセットする。
func (b *Bridge) FilterReset(channel, mode uint8) error {
	_, err := b.Send(ReqFilterReset, uint16(mode)<<8|uint16(channel), 0, 1)
	return err
}

// FilterRead は TS フィルター（チャンネル 0）のレジスタ reg を読む。
func (b *Bridge) FilterRead(reg uint8) (uint8, error) {
	data, err := b.Send(ReqFilterRead, uint16(reg), 0, 2)
	if err != nil {
		return 0, err
	}
	if len(data) < 2 {
		return 0, fmt.Errorf("TS フィルターの読み（%#02x）の答えが %d バイト", reg, len(data))
	}
	return data[1], nil
}

// FilterWrite8 は TS フィルターの 8 ビットのレジスタ reg に value を書く。
func (b *Bridge) FilterWrite8(reg, value uint8) error {
	_, err := b.Send(ReqFilterWrite, uint16(value)<<8|uint16(reg), 0, 2)
	return err
}

// FilterWrite16 は TS フィルターの 16 ビットのレジスタ reg（上位）と reg+1（下位）に value を書く。
func (b *Bridge) FilterWrite16(reg uint8, value uint16) error {
	_, err := b.Send(ReqFilterWrite, value&0xff00|uint16(reg), value&0xff, 3)
	return err
}

// StreamStart は TS のチャンネル channel を流し始める。
func (b *Bridge) StreamStart(channel uint8) error {
	_, err := b.Send(ReqStreamStart, uint16(channel), 0, 1)
	return err
}

// StreamStop は TS のチャンネル channel を止める。
func (b *Bridge) StreamStop(channel uint8) error {
	_, err := b.Send(ReqStreamStop, uint16(channel), 0, 1)
	return err
}
