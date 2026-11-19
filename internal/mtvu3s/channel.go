// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package mtvu3s

import (
	"fmt"
	"strconv"
	"strings"
)

// Band は放送の帯。
type Band int

const (
	BandUHF Band = iota // 地上波
	BandBS
	BandCS // 110 度 CS
)

func (b Band) String() string {
	switch b {
	case BandUHF:
		return "UHF"
	case BandBS:
		return "BS"
	case BandCS:
		return "CS"
	}
	return fmt.Sprintf("Band(%d)", int(b))
}

// チャンネルから周波数を出す定数（kHz。BS・CS は第一 IF）
const (
	uhfBaseKHz = 473143 // ch13 の中心
	uhfStepKHz = 6000
	bsBaseKHz  = 1049480 // BS1
	bsStepKHz  = 38360
	csBaseKHz  = 1613000 // ND2
	csStepKHz  = 40000
)

// Channel は選局する先。
type Channel struct {
	Name    string
	Band    Band
	FreqKHz uint32
	// TSID は衛星で選ぶ TS。0 なら TMCC にある最初のもの（CS はいつも 0）
	TSID uint16
}

// ParseChannel はチャンネルの書き方を読む（recfriio の --u3s と同じ）。
//
//	13〜62    UHF
//	BSnn_s    BS（nn は 1〜23 の奇数、s は 0〜7）
//	CSnn      110 度 CS（nn は 2〜24 の偶数）
func ParseChannel(s string) (Channel, error) {
	up := strings.ToUpper(s)
	switch {
	case strings.HasPrefix(up, "BS"):
		node, slot, ok := strings.Cut(up[2:], "_")
		n, err := strconv.Atoi(node)
		if !ok || err != nil || n < 1 || n > 23 || n%2 == 0 {
			return Channel{}, fmt.Errorf("BS のチャンネル %q: BS の番号は 1〜23 の奇数（例: BS15_0）", s)
		}
		sl, err := strconv.Atoi(slot)
		if err != nil || len(slot) != 1 || sl < 0 || sl > 7 {
			return Channel{}, fmt.Errorf("BS のチャンネル %q: スロットは 0〜7（例: BS15_0）", s)
		}
		// ARIB の決まりで BS15 は 1 つずれる（recfriio と同じ。古い受信機への配慮）
		if n == 15 {
			sl++
		}
		return Channel{
			Name:    fmt.Sprintf("BS%02d_%s", n, slot),
			Band:    BandBS,
			FreqKHz: bsBaseKHz + uint32((n-1)/2)*bsStepKHz,
			TSID:    uint16(0x4000 | n<<4 | sl),
		}, nil
	case strings.HasPrefix(up, "CS"):
		n, err := strconv.Atoi(up[2:])
		if err != nil || n < 2 || n > 24 || n%2 != 0 {
			return Channel{}, fmt.Errorf("CS のチャンネル %q: 2〜24 の偶数（例: CS2）", s)
		}
		return Channel{
			Name:    fmt.Sprintf("CS%d", n),
			Band:    BandCS,
			FreqKHz: csBaseKHz + uint32(n/2-1)*csStepKHz,
		}, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 13 || n > 62 {
		return Channel{}, fmt.Errorf("チャンネル %q が読めない（13〜62、BS15_0、CS2 など）", s)
	}
	return Channel{
		Name:    strconv.Itoa(n),
		Band:    BandUHF,
		FreqKHz: uhfBaseKHz + uint32(n-13)*uhfStepKHz,
	}, nil
}
