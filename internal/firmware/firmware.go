// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// Package firmware は QRS-UT100B（ASICEN ASV5211）のファームを、付属 CD のドライバーから取り出して送る
// （recfriio の tools/ut100b-firmware.py を Go にしたもの）。
//
// ファームはメーカーの著作物なので、このリポジトリには入れていない。
// 製品に付いていた CD の Quixun_AS11Loader.sys から、使う人が自分で取り出す。
package firmware

import (
	"crypto/aes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/waya0125/recfriio/internal/usbfs"
)

// Size はファームの大きさ。
const Size = 0x4000

// Loader は知っている Quixun_AS11Loader.sys。ファームは .data の中に 2 つ、各バイトに 1 を足したうえで
// AES-128-ECB で暗号化されている。ファームの 8 バイト前に起動アドレス（リトルエンディアン 2 バイト）。
// 1 つ目がローダーの送る主のファーム、2 つ目は古い版（送られないもの）。
type Loader struct {
	Name   string
	Images [2]int
}

// KnownLoaders は SHA-256 から Loader を引く。
var KnownLoaders = map[string]Loader{
	"77244d1065de8893872a1cc5eac94b08c3350c916bc8743904df4a31ddeec6dd": {
		Name: "ROBRO Origin 版 Quixun_AS11Loader.sys 1.08.11.17 (2008-11-17)", Images: [2]int{0x52d8, 0x92e0}},
	"e0438aef67141e2d9e4f0c91f273d979f27ebf17386344479836632969868261": {
		Name: "ROBRO Origin+ (forNetbook) 版 Quixun_AS11Loader.sys 1.09.02.19 (2009-02-19)", Images: [2]int{0x5558, 0x9560}},
}

var loaderKey, _ = hex.DecodeString("277883a707d748ed8e12abfb89ccbd77")

// Image は知っているファーム。
type Image struct {
	// Index は起動アドレス。ファームの版で決まる（機種ではない）。違うアドレスで起動すると抜き差しが要る
	Index uint16
	Name  string
}

// KnownImages は SHA-256 から Image を引く。
var KnownImages = map[string]Image{
	"1f70eb5934456b3b78b0c5ff09a1713e55449cfcd40cc3bcc6a9762f00ec08a0": {0x5399, "Quixun 1.08.11.17 の主のファーム"},
	"50d406d570a9357451a1911645bc3ec1bd64c4e59f9919259d3144efafcc38a4": {0x53e4, "Quixun 1.09.02.19 (forNetbook) の主のファーム"},
	"d134890df91427d405ad5619588e63e3220e21f972ede18a9701fec5686b4816": {0x515f, "古い版（Quixun・PLEX の両方のローダーが持つ、送られないもの）"},
	"b45d510200a1690b3ca358d93de13f40e1d3567b663c17e773349ad96f597aa8": {0x5399, "PLEX の Linux ドライバー loader.ko のファーム"},
}

// ブート ROM は 4 回のベンダーリクエストでファームを受け取り、最後の 0xac で起動する。
// 0x0c00..0x2000 と 0x2400..0x2800 は送らない（どのファームでも 0 で埋まっている）。
var segments = []struct {
	offset, length int
	request        uint8
}{{0x0000, 0x0c00, 0xab}, {0x2000, 0x0400, 0xab}, {0x2800, 0x1000, 0xab}, {0x3800, 0x0800, 0xac}}

var holes = [][2]int{{0x0c00, 0x2000}, {0x2400, 0x2800}}

// Digest は SHA-256 の 16 進。
func Digest(p []byte) string {
	sum := sha256.Sum256(p)
	return hex.EncodeToString(sum[:])
}

// LooksLikeFirmware は image がファームの形（大きさと、送らない所が 0）をしているか。
func LooksLikeFirmware(image []byte) bool {
	if len(image) != Size {
		return false
	}
	for _, h := range holes {
		for _, b := range image[h[0]:h[1]] {
			if b != 0 {
				return false
			}
		}
	}
	return true
}

// Extract は Quixun_AS11Loader.sys の中身 loader から、which 番目（0 が主、1 が古い版）のファームを取り出す。
func Extract(loader []byte, which int) (image []byte, index uint16, info Image, loaderName string, err error) {
	digest := Digest(loader)
	known, ok := KnownLoaders[digest]
	if !ok {
		return nil, 0, Image{}, "", fmt.Errorf("知らないファイル（SHA-256 %s）。付属 CD の Driver フォルダーにある Quixun_AS11Loader.sys を渡す", digest)
	}
	offset := known.Images[which]
	index = binary.LittleEndian.Uint16(loader[offset-8:])
	sealed := make([]byte, Size)
	for i := range sealed {
		sealed[i] = loader[offset+i] - 1
	}
	block, err := aes.NewCipher(loaderKey)
	if err != nil {
		return nil, 0, Image{}, "", err
	}
	image = make([]byte, Size)
	for i := 0; i < Size; i += aes.BlockSize {
		block.Decrypt(image[i:], sealed[i:])
	}
	info, ok = KnownImages[Digest(image)]
	if !ok || info.Index != index || !LooksLikeFirmware(image) {
		return nil, 0, Image{}, "", errors.New("取り出したファームが想定と違う（復号の誤り）。書き出さずに止める")
	}
	return image, index, info, known.Name, nil
}

// Controller はファームを送る先（usbfs.Device）。
type Controller interface {
	Control(requestType, request uint8, value, index uint16, data []byte, timeoutMS uint32) (int, error)
}

// Send はファームの無い ASV5211 に image を送り、起動アドレス index から動かす。
func Send(dev Controller, image []byte, index uint16) error {
	if !LooksLikeFirmware(image) {
		return errors.New("ASV52xx のファームの形をしていない")
	}
	for _, s := range segments {
		data := append([]byte{}, image[s.offset:s.offset+s.length]...)
		n, err := dev.Control(0x40, s.request, uint16(s.offset), index, data, 1000)
		if err != nil {
			return fmt.Errorf("%#04x からの送信: %w", s.offset, err)
		}
		if n != s.length {
			return fmt.Errorf("%#04x からの送信が %d バイトで止まった（%d バイトのはず）", s.offset, n, s.length)
		}
	}
	return nil
}

// IndexFor は image の起動アドレスを返す。知らないファームなら ok が偽。
func IndexFor(image []byte) (uint16, bool) {
	info, ok := KnownImages[Digest(image)]
	return info.Index, ok
}

// Load はファームの無い UT100B（cold）を探して image を送り、warm として出直すのを待つ。
// 出直した機器ファイルを返す。
func Load(image []byte, index uint16, cold, warm usbfs.ID, timeout time.Duration) (string, error) {
	paths, err := usbfs.Find(cold)
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("ファームの無い UT100B（%s）が見つからない", cold)
	}
	before, _ := usbfs.Find(warm)
	dev, err := usbfs.Open(paths[0])
	if err != nil {
		return "", err
	}
	err = Send(dev, image, index)
	dev.Close()
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		now, _ := usbfs.Find(warm)
		for _, p := range now {
			if !contains(before, p) {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("送ったが、%v 待っても %s として出てこない", timeout, warm)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
