// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package mtvu3s

import (
	"bytes"
	"crypto/aes"
	"testing"

	"github.com/waya0125/recfriio/internal/tc90522"
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
		{"CS2", BandCS, 1613000, 0x6020},
		{"CS24", BandCS, 1613000 + 11*40000, 0x7180},
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

func TestAESDescrambler(t *testing.T) {
	var key [16]byte
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, PacketSize)
	for i := range plain {
		plain[i] = byte(i)
	}
	plain[0] = 0x47

	enc := append([]byte(nil), plain...)
	for b := 0; b < tsAESBlocks; b++ {
		off := tsAESOffset + b*aes.BlockSize
		block.Encrypt(enc[off:off+aes.BlockSize], enc[off:off+aes.BlockSize])
	}

	d, err := NewDescrambler(key)
	if err != nil {
		t.Fatal(err)
	}
	d.DescramblePacket(enc)
	if !bytes.Equal(enc, plain) {
		t.Fatal("AES-128-ECB 復号結果が元のパケットと一致しない")
	}
}

func TestMatchTSID(t *testing.T) {
	// 2026-11-19 の BS23 の実機の一覧（スロット 0・1・3。登録年のビットがスロットごとに違う）
	ids := []uint16{0x4770, 0x4971, 0x4973, 0xfff9}
	cases := []struct {
		want, got uint16
		ok        bool
	}{
		{0x4770, 0x4770, true},  // そのまま
		{0x4171, 0x4971, true},  // BS の登録年のビットを無視して当てる
		{0x4172, 0x4172, false}, // BS23_2 はいま無い
		{0x6020, 0x6020, false}, // CS は登録年を無視しない
		{0, 0, true},
	}
	for _, c := range cases {
		if got, ok := MatchTSID(c.want, ids); got != c.got || ok != c.ok {
			t.Errorf("MatchTSID(%#04x) = %#04x, %v; want %#04x, %v", c.want, got, ok, c.got, c.ok)
		}
	}
}

func TestSettler(t *testing.T) {
	s := &Settler{Clean: 3, MaxDrop: 100}
	tei := func(cc uint8) []byte {
		p := packet(0x100, cc)
		p[1] |= 0x80
		return p
	}
	var in []byte
	in = append(in, tei(0)...)
	in = append(in, packet(0x100, 1)...)
	in = append(in, tei(2)...) // ここまで捨てる
	in = append(in, packet(0x100, 3)...)
	in = append(in, packet(0x100, 4)...)
	out := s.Feed(in)
	if out != nil || s.Settled() {
		t.Fatalf("まだ流さないはず: %d バイト", len(out))
	}
	out = s.Feed(append(packet(0x100, 5), packet(0x100, 6)...))
	if !s.Settled() || len(out) != 4*PacketSize || out[3]&0x0f != 3 {
		t.Fatalf("settled=%v len=%d", s.Settled(), len(out))
	}
	if s.Dropped != 3 {
		t.Fatalf("dropped=%d", s.Dropped)
	}
	// 落ち着いたあとは TEI が付いていても全部流す
	if out := s.Feed(tei(7)); len(out) != PacketSize {
		t.Fatal("落ち着いたあとに捨てている")
	}

	g := &Settler{Clean: 1000, MaxDrop: 4}
	var bad []byte
	for i := range 6 {
		bad = append(bad, tei(uint8(i))...)
	}
	if out := g.Feed(bad); !g.GaveUp || len(out) != 2*PacketSize {
		t.Fatalf("gaveup=%v len=%d", g.GaveUp, len(out))
	}
}
