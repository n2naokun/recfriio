// SPDX-License-Identifier: MIT
// Copyright (c) 2026 KASHIWAYA Rikuto

package ut100b

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/waya0125/recut100b/internal/asv5211"
	"github.com/waya0125/recut100b/internal/usbfs"
)

// USB の ID
var (
	WarmID = usbfs.ID{Vendor: 0x3275, Product: 0x7020} // ファームが入った状態
	ColdID = usbfs.ID{Vendor: 0x1738, Product: 0x5211} // ファームの無い状態（ASV5211 のブート ROM）
)

// 受信の設定（recfriio の HDP の設定と同じ）
const (
	streamEndpoint = 0x81
	urbCount       = 32
	urbSize        = 16384
	// 溜めておける量。平均 16 Mbps で 20 秒ぶん（recfriio の ASYNCBUFFTIME と同じ）
	queueChunks = 0x200000 * 20 / urbSize
)

// ErrNoDevice は使える UT100B が無いこと。
var ErrNoDevice = errors.New("使える QRS-UT100B（3275:7020）が見つからない")

// Device は開いた UT100B。
type Device struct {
	USB    *usbfs.Device
	Bridge *asv5211.Bridge
	Tuner  *Tuner
}

// OpenDevice は path の UT100B を開いてインターフェースを取る。path が空なら、
// 見つかったもののうち、ほかのプロセスが使っていない最初のものを開く。
func OpenDevice(path string) (*Device, error) {
	paths := []string{path}
	if path == "" {
		found, err := usbfs.Find(WarmID)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, ErrNoDevice
		}
		paths = found
	}
	var last error
	for _, p := range paths {
		dev, err := open(p)
		if err == nil {
			return dev, nil
		}
		last = err
		if !errors.Is(err, syscall.EBUSY) {
			break
		}
	}
	return nil, last
}

func open(path string) (*Device, error) {
	id, err := usbfs.ReadID(path)
	if err != nil {
		return nil, err
	}
	if id != WarmID {
		return nil, fmt.Errorf("%s は %s（UT100B は %s）", path, id, WarmID)
	}
	usb, err := usbfs.Open(path)
	if err != nil {
		return nil, err
	}
	if err := usb.Claim(0); err != nil {
		usb.Close()
		return nil, err
	}
	if err := usb.SetInterface(0, 0); err != nil {
		usb.Close()
		return nil, err
	}
	bridge := asv5211.New(usb)
	return &Device{USB: usb, Bridge: bridge, Tuner: NewTuner(bridge)}, nil
}

// StartStream は TS の受信を始める（Tune で ASV5211 側の流し始めは済んでいる）。
func (d *Device) StartStream() (*usbfs.Stream, error) {
	return d.USB.StartStream(streamEndpoint, urbCount, urbSize, queueChunks)
}

// Close はチューナーを閉じ（開いていれば）、USB 機器を返す。
func (d *Device) Close() error {
	err := d.Tuner.Close()
	if cerr := d.USB.Close(); err == nil {
		err = cerr
	}
	return err
}
