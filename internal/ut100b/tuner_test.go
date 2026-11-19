// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package ut100b

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/waya0125/recfriio/internal/asv5211"
)

// fakeUT100B は ASV5211 の偽物。送られたリクエストを記録し、I2C の機器のレジスタを覚えておく。
// 0x5a には誰もいない（UT100B の ASIE5606 は 0x4a で答える）。
type fakeUT100B struct {
	sent      []string
	registers map[uint16]uint8 // addr << 8 | reg
	filter    map[uint8]uint8
}

func newFake() *fakeUT100B {
	return &fakeUT100B{
		registers: map[uint16]uint8{I2CCardHDP<<8 | cardIdentity: 0x06},
		filter:    map[uint8]uint8{},
	}
}

func (f *fakeUT100B) Control(requestType, request uint8, value, index uint16, data []byte, timeoutMS uint32) (int, error) {
	f.sent = append(f.sent, fmt.Sprintf("%02x %04x %04x %d", request, value, index, len(data)))
	if len(data) > 0 {
		data[0] = 1
	}
	addr, reg := uint8(value), uint8(value>>8)
	switch request {
	case asv5211.ReqRegRead:
		if addr != I2CDemod && addr != I2CCardHDP {
			data[0] = 0
			break
		}
		data[1] = f.registers[uint16(addr)<<8|uint16(reg)]
	case asv5211.ReqRegWrite:
		f.registers[uint16(addr)<<8|uint16(reg)] = uint8(index)
	case asv5211.ReqFilterRead:
		data[1] = f.filter[uint8(value)]
	case asv5211.ReqFilterWrite:
		f.filter[uint8(value)] = uint8(value >> 8)
	}
	return len(data), nil
}

func readTrace(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var req, val, idx, length int
		if _, err := fmt.Sscanf(line, "%x %x %x %d", &req, &val, &idx, &length); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		lines = append(lines, fmt.Sprintf("%02x %04x %04x %d", req, val, idx, length))
	}
	return lines
}

// recfriio の --hdp 19 が開く・選局・C/N を 2 回読む・閉じるまでに送った制御転送と、同じ並びになるか。
func TestSameRequestsAsRecfriio(t *testing.T) {
	want := readTrace(t, "../../testdata/usb-trace-hdp-ch19.txt")
	fake := newFake()
	tuner := NewTuner(asv5211.New(fake))
	tuner.Sleep = func(time.Duration) {}
	if err := tuner.Open(); err != nil {
		t.Fatal(err)
	}
	freq, err := Frequency(19, false)
	if err != nil || freq != 509 {
		t.Fatalf("Frequency(19) = %d, %v", freq, err)
	}
	if err := tuner.Tune(freq); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := tuner.CNR(); err != nil {
			t.Fatal(err)
		}
	}
	if err := tuner.Close(); err != nil {
		t.Fatal(err)
	}
	if tuner.CardAddr != I2CCardHDP {
		t.Errorf("ASIE5606 のアドレス %#02x", tuner.CardAddr)
	}
	got := fake.sent
	for i := 0; i < len(want) || i < len(got); i++ {
		var w, g string
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if w != g {
			t.Fatalf("%d 番目が違う: recfriio %q、Go %q（recfriio %d 回、Go %d 回）", i+1, w, g, len(want), len(got))
		}
	}
}

func TestFrequency(t *testing.T) {
	cases := []struct {
		ch   int
		catv bool
		mhz  int
	}{{13, false, 473}, {62, false, 767}, {13, true, 111}, {22, true, 165}, {23, true, 225}, {24, true, 233}, {27, true, 251}, {28, true, 255}, {63, true, 465}}
	for _, c := range cases {
		got, err := Frequency(c.ch, c.catv)
		if err != nil || got != c.mhz {
			t.Errorf("Frequency(%d, %v) = %d, %v; want %d", c.ch, c.catv, got, err, c.mhz)
		}
	}
	for _, ch := range []int{12, 63} {
		if _, err := Frequency(ch, false); err == nil {
			t.Errorf("UHF %d が通った", ch)
		}
	}
}

func TestCNRFromRaw(t *testing.T) {
	// 実機の値（recfriio の記録）: raw 0x1807 → 31.0526 dB、0x14fb → 31.7592 dB
	for _, c := range []struct {
		raw  uint32
		want float64
	}{{0x1807, 31.0526}, {0x14fb, 31.7592}, {0, 0}} {
		got := CNRFromRaw(c.raw)
		if got < c.want-0.0001 || got > c.want+0.0001 {
			t.Errorf("CNRFromRaw(%#x) = %v; want %v", c.raw, got, c.want)
		}
	}
}
