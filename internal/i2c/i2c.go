// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// Package i2c は、チューナーや復調部が話す I2C のバスの最小の口。
// EM28178 の I2C と、TC90522 の中継（0xfe）を通る先の I2C を同じ形で扱う。
package i2c

import "time"

// Bus は I2C のバス。addr は 8 ビットの書き方（0xc6 など、bit 0 は読み書きの区別に使わない）。
// stop が false なら、最後に停止を送らない（続けて読むときの repeated start）。
type Bus interface {
	Write(addr uint8, data []byte, stop bool) error
	Read(addr uint8, data []byte, stop bool) error
}

// RegVal はレジスタの番号と値の組。
type RegVal struct{ Reg, Val uint8 }

// WaitUntil は interval だけ待ってから pred を見る、を最大 tries 回くり返す。
// pred が true を返せば true。pred のエラーはそのまま返す。
func WaitUntil(pred func() (bool, error), tries int, interval time.Duration) (bool, error) {
	for range tries {
		time.Sleep(interval)
		ok, err := pred()
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}
