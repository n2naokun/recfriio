// SPDX-License-Identifier: MIT
// Copyright (c) 2026 KASHIWAYA Rikuto

package ut100b

import (
	"bytes"
	"testing"
)

func packets(n int, pid uint16) []byte {
	var b []byte
	for i := range n {
		p := make([]byte, packetSize)
		p[0], p[1], p[2], p[3] = 0x47, byte(pid>>8), byte(pid), 0x10|byte(i&15)
		for j := 4; j < packetSize; j++ {
			p[j] = byte(i + j)
		}
		b = append(b, p...)
	}
	return b
}

func TestAlignerDropsStaleAndSplitsAnywhere(t *testing.T) {
	stale := bytes.Repeat([]byte{0x55}, StaleDiscardBytes)
	body := packets(50, 0x100)
	input := append(append([]byte{}, stale...), body...)
	a := NewAligner()
	var got []byte
	for len(input) > 0 {
		n := min(len(input), 1000) // USB の区切りは 188 の倍数ではない
		got = append(got, a.Feed(input[:n])...)
		input = input[n:]
	}
	if len(got) != len(body) {
		t.Fatalf("%d バイト出た（%d のはず）", len(got), len(body))
	}
	want := make([]byte, len(body))
	for i := 0; i < len(body); i += packetSize {
		a.d.DecryptPacket(want[i:], body[i:i+packetSize])
	}
	if !bytes.Equal(got, want) {
		t.Fatal("中身が違う")
	}
	if a.Resyncs != 0 {
		t.Fatalf("再同期 %d 回", a.Resyncs)
	}
}

func TestAlignerResyncs(t *testing.T) {
	a := NewAligner()
	a.stale = 0
	body := packets(10, 0x100)
	input := append([]byte{0x00, 0x47, 0x00}, body...) // ごみの中に 0x47 があっても、188 先が 0x47 でなければ飛ばす
	got := a.Feed(input)
	if len(got) != len(body) || a.Resyncs != 1 {
		t.Fatalf("%d バイト、再同期 %d 回", len(got), a.Resyncs)
	}
}
