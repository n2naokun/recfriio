// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package mtvu3s

import (
	"bytes"
	"testing"

	"github.com/waya0125/recut100b/internal/tc90522"
)

func TestParseChannel(t *testing.T) {
	cases := []struct {
		in   string
		band Band
		freq uint32
		tsid uint16
	}{
		{"13", BandUHF, 473143, 0},
		{"21", BandUHF, 521143, 0},
		{"62", BandUHF, 767143, 0},
		{"BS01_0", BandBS, 1049480, 0x4010},
		{"bs15_0", BandBS, 1049480 + 7*38360, 0x40f1}, // BS15 は 1 つずれる
		{"BS23_2", BandBS, 1049480 + 11*38360, 0x4172},
		{"CS2", BandCS, 1613000, 0},
		{"CS24", BandCS, 1613000 + 11*40000, 0},
	}
	for _, c := range cases {
		got, err := ParseChannel(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got.Band != c.band || got.FreqKHz != c.freq || got.TSID != c.tsid {
			t.Errorf("%s = %+v", c.in, got)
		}
	}
	for _, bad := range []string{"12", "63", "BS2_0", "BS25_0", "BS15_8", "BS15", "CS1", "CS26", "C13", "x"} {
		if _, err := ParseChannel(bad); err == nil {
			t.Errorf("%s が通ってしまう", bad)
		}
	}
}

func packet(pid uint16, cc uint8) []byte {
	p := make([]byte, PacketSize)
	p[0] = 0x47
	p[1] = uint8(pid >> 8)
	p[2] = uint8(pid)
	p[3] = 0x10 | cc
	return p
}

type countDescrambler struct{ n int }

func (c *countDescrambler) DescramblePacket(p []byte) { c.n++ }

func TestAligner(t *testing.T) {
	var stream []byte
	stream = append(stream, 0x00, 0x47, 0x12) // ゴミ
	for i := range 10 {
		stream = append(stream, packet(0x100, uint8(i))...)
	}
	a := NewAligner()
	d := &countDescrambler{}
	a.Descrambler = d
	var out []byte
	// 半端な大きさで分けて流す
	for len(stream) > 0 {
		n := min(97, len(stream))
		out = append(out, a.Feed(stream[:n])...)
		stream = stream[n:]
	}
	if len(out) != 10*PacketSize {
		t.Fatalf("%d バイト出た（10 パケットのはず）", len(out))
	}
	if !bytes.Equal(out[:PacketSize], packet(0x100, 0)) {
		t.Fatal("最初のパケットがずれている")
	}
	if a.Skipped != 3 || d.n != 10 || a.BadSync != 0 {
		t.Fatalf("skipped=%d descrambled=%d badsync=%d", a.Skipped, d.n, a.BadSync)
	}
}

func TestAlignerResync(t *testing.T) {
	var stream []byte
	for i := range 4 {
		stream = append(stream, packet(0x100, uint8(i))...)
	}
	stream = append(stream, 0xaa, 0xbb) // 途中で 2 バイト入る
	for i := 4; i < 10; i++ {
		stream = append(stream, packet(0x100, uint8(i))...)
	}
	a := NewAligner()
	var out []byte
	out = append(out, a.Feed(stream[:4*PacketSize+100])...)
	out = append(out, a.Feed(stream[4*PacketSize+100:])...)
	// ずれたまま出たあと、続きでつかみ直す
	var more []byte
	for i := 10; i < 13; i++ {
		more = append(more, packet(0x100, uint8(i))...)
	}
	out = append(out, a.Feed(more)...)
	if a.BadSync == 0 || a.Resyncs != 1 {
		t.Fatalf("badsync=%d resyncs=%d", a.BadSync, a.Resyncs)
	}
	// つかみ直したあとの最後のパケットは正しく出る
	if !bytes.Equal(out[len(out)-PacketSize:], packet(0x100, 12)) {
		t.Fatal("つかみ直したあとのパケットがずれている")
	}
}

func TestCNR(t *testing.T) {
	if got := tc90522.TerCNRFromRaw(0); got != 0 {
		t.Errorf("TerCNRFromRaw(0) = %v", got)
	}
	// 生の値が小さいほど C/N は高い
	if hi, lo := tc90522.TerCNRFromRaw(0x1000), tc90522.TerCNRFromRaw(0x40000); hi <= lo {
		t.Errorf("TerCNRFromRaw: %v <= %v", hi, lo)
	}
	if got := tc90522.SatCNRFromRaw(2999); got != 0 {
		t.Errorf("SatCNRFromRaw(2999) = %v", got)
	}
}
