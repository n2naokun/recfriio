// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// recfriio は QRS-UT100B（地デジ）と SKNET MonsterTV U3S（地デジ・BS・CS）で受けて TS を書き出す。
// 引数の形は C++ 版の recfriio・recpt1 と同じ（チャンネル 秒 出力先）。機種は --hdp・--u3s・--device・チャンネルから選ぶ。
package main

import (
	"os"

	"github.com/waya0125/recfriio/internal/cli"
)

func main() {
	os.Exit(cli.Main("recfriio", cli.Any, os.Args[1:]))
}
