// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// Package tscheck は TS のパケットのヘッダー（先頭 4 バイトと、適応フィールドの頭）だけを見て、
// 受信の乱れを数える。ペイロードは見ないので、ペイロードに暗号やスクランブルがかかったままでも数えられる。
package tscheck

import (
	"fmt"
	"io"
	"sort"
)

const packetSize = 188

// Stats は数えた結果。
type Stats struct {
	Packets   int
	SyncError int // 先頭が 0x47 でない
	TEI       int // transport_error_indicator が立っている
	CCError   int // continuity_counter が飛んだ（重複 1 回と、不連続の印は除く）
	PIDs      map[uint16]int
	lastCC    map[uint16]int
}

// New は空の Stats を作る。
func New() *Stats {
	return &Stats{PIDs: map[uint16]int{}, lastCC: map[uint16]int{}}
}

// Add は 188 の倍数の長さの packets を数える。
func (s *Stats) Add(packets []byte) {
	for p := 0; p+packetSize <= len(packets); p += packetSize {
		s.add(packets[p : p+packetSize])
	}
}

func (s *Stats) add(pkt []byte) {
	s.Packets++
	if pkt[0] != 0x47 {
		s.SyncError++
		return
	}
	if pkt[1]&0x80 != 0 {
		s.TEI++
		return
	}
	pid := uint16(pkt[1]&0x1f)<<8 | uint16(pkt[2])
	s.PIDs[pid]++
	if pid == 0x1fff {
		return
	}
	afc := pkt[3] >> 4 & 0x03
	cc := int(pkt[3] & 0x0f)
	discontinuity := afc&0x02 != 0 && pkt[4] > 0 && pkt[5]&0x80 != 0
	last, seen := s.lastCC[pid]
	if afc&0x01 == 0 {
		// ペイロードの無いパケットはカウンターを進めない
		return
	}
	s.lastCC[pid] = cc
	if !seen || discontinuity {
		return
	}
	if cc != (last+1)&0x0f && cc != last {
		s.CCError++
	}
}

// Print は結果を w に書く。PID は多い順に top 個まで。
func (s *Stats) Print(w io.Writer, top int) {
	fmt.Fprintf(w, "packets=%d sync_err=%d tei=%d cc_err=%d pids=%d\n", s.Packets, s.SyncError, s.TEI, s.CCError, len(s.PIDs))
	pids := make([]uint16, 0, len(s.PIDs))
	for pid := range s.PIDs {
		pids = append(pids, pid)
	}
	sort.Slice(pids, func(i, j int) bool {
		if s.PIDs[pids[i]] != s.PIDs[pids[j]] {
			return s.PIDs[pids[i]] > s.PIDs[pids[j]]
		}
		return pids[i] < pids[j]
	})
	for i, pid := range pids {
		if i >= top {
			break
		}
		fmt.Fprintf(w, "  pid %#04x: %d\n", pid, s.PIDs[pid])
	}
}
