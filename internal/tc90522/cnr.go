// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package tc90522

import "math"

// TerCNRFromRaw は地上波の C/N の生の値（0x8b..0x8d）を dB にする。0 なら 0。
// 式は Linux の tc90522 と同じ（UT100B の internal/ut100b.CNRFromRaw とも同じ）。
// recfriio の Tc90522.cpp は 10*log10(2^24/raw) を使っていたが、Linux・coax と違うので揃えた。
func TerCNRFromRaw(raw uint32) float64 {
	if raw == 0 {
		return 0
	}
	p := 10 * math.Log10(5505024/float64(raw))
	cnr := 0.000024*p*p*p*p - 0.0016*p*p*p + 0.0398*p*p + 0.5491*p + 3.0965
	return max(cnr, 0)
}

// SatCNRFromRaw は衛星の C/N の生の値（0xbc..0xbd）を dB にする。3000 より小さければ 0。
// 式は recfriio の Tc90522.cpp と同じ。
func SatCNRFromRaw(raw uint32) float64 {
	if raw < 3000 {
		return 0
	}
	p := math.Sqrt(float64(raw-3000)) / 64
	p2 := p * p
	p3 := p2 * p
	p4 := p2 * p2
	p5 := p4 * p
	cnr := (-1634.6*p5 + 14341.0*p4 - 50259.0*p3 + 88977.0*p2 - 89565.0*p + 58857.0) / 1000
	return max(cnr, 0)
}
