// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package firmware

import (
	"fmt"
	"testing"
)

type recorder struct{ sent []string }

func (r *recorder) Control(requestType, request uint8, value, index uint16, data []byte, timeoutMS uint32) (int, error) {
	r.sent = append(r.sent, fmt.Sprintf("%02x %02x %04x %04x %d", requestType, request, value, index, len(data)))
	return len(data), nil
}

// ブート ROM に送るリクエストが、Python 版・PLEX の loader.ko と同じ 4 回になるか。
func TestSendSegments(t *testing.T) {
	image := make([]byte, Size)
	image[0], image[0x3fff] = 1, 2
	r := &recorder{}
	if err := Send(r, image, 0x5399); err != nil {
		t.Fatal(err)
	}
	want := []string{"40 ab 0000 5399 3072", "40 ab 2000 5399 1024", "40 ab 2800 5399 4096", "40 ac 3800 5399 2048"}
	if fmt.Sprint(r.sent) != fmt.Sprint(want) {
		t.Fatalf("%v", r.sent)
	}
}

func TestLooksLikeFirmware(t *testing.T) {
	image := make([]byte, Size)
	if !LooksLikeFirmware(image) {
		t.Fatal("0 のファームが通らない")
	}
	image[0x0c00] = 1
	if LooksLikeFirmware(image) {
		t.Fatal("送らない所が 0 でないのに通った")
	}
	if LooksLikeFirmware(make([]byte, 100)) {
		t.Fatal("大きさの違うものが通った")
	}
	if _, _, _, _, err := Extract([]byte("not a loader"), 0); err == nil {
		t.Fatal("知らないローダーが通った")
	}
}
