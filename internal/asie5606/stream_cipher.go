// SPDX-License-Identifier: MIT
// Copyright (c) 2026 KASHIWAYA Rikuto

// Package asie5606 は ASICEN ASIE5606（QRS-UT100B の B-CAS カードの IC）を扱う。
// USB 転送の暗号を外す処理（stream_cipher.go）と、内蔵の B-CAS スロット（card.go）。
package asie5606

import (
	"crypto/cipher"
	"crypto/des"
)

// ASIE5606 は、復調器から受けた TS をブリッジ（ASV5211）へ渡すとき、各パケットに DES をかける。
// これは基板の中の IC どうしの転送にかかる暗号で、放送の暗号（ARIB STD-B25、MULTI2）とは別のもの。
// ここで外したあとの TS は、放送のスクランブルがかかったまま。
// 放送のスクランブルを外すのは internal/b25 で、ECM とワーク鍵の処理は B-CAS カードが受け持つ。
// レジスタの設定では止められなかった（docs/QRS-UT100B.md）。
//
// 外し方:
//  1. 4 バイトのヘッダーはそのまま。
//  2. 残りの 184 バイトに、8 バイト周期の値 whitening を XOR する。
//  3. 先頭の 128 バイトを DES 鍵 keyHead で、残りの 56 バイトを DES 鍵 keyTail で、ECB で復号する。
//
// 鍵は ASIE5606 の 0x10..0x1f が全部 0 のときのもの（いつも 0 にしている）。
// DES は標準のもの（Go の crypto/des）。
var (
	whitening = [8]byte{0x00, 0x00, 0xb1, 0xf2, 0x00, 0x04, 0xf2, 0x04}
	keyHead   = []byte{0xbc, 0x8c, 0x73, 0xe9, 0x0e, 0x68, 0x31, 0x23}
	keyTail   = []byte{0xec, 0x15, 0x80, 0x01, 0x80, 0x7f, 0x80, 0x80}
)

// PacketSize は TS のパケットの大きさ。
const PacketSize = 188

// StreamCipher は ASIE5606 が USB 転送でかける DES を外す。
type StreamCipher struct {
	head, tail cipher.Block
}

// NewStreamCipher は StreamCipher を作る。
func NewStreamCipher() *StreamCipher {
	head, err := des.NewCipher(keyHead)
	if err != nil {
		panic(err)
	}
	tail, err := des.NewCipher(keyTail)
	if err != nil {
		panic(err)
	}
	return &StreamCipher{head: head, tail: tail}
}

// DecryptPacket は 188 バイトのパケット src の DES を外して dst に書く（dst と src は同じでもよい）。
// B25 のスクランブルはそのまま残る。
func (d *StreamCipher) DecryptPacket(dst, src []byte) {
	_ = dst[PacketSize-1]
	_ = src[PacketSize-1]
	var block [8]byte
	copy(dst[:4], src[:4])
	for i := 4; i < PacketSize; i += 8 {
		n := min(8, PacketSize-i)
		for j := range n {
			block[j] = src[i+j] ^ whitening[j]
		}
		if i < 4+128 {
			d.head.Decrypt(block[:], block[:])
		} else {
			d.tail.Decrypt(block[:], block[:])
		}
		copy(dst[i:i+n], block[:n])
	}
}
