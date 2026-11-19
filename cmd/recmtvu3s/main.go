// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// recmtvu3s は recfriio を MonsterTV U3S に決めて動かす（前からある名前を残すためのもの）。
// recfriio --u3s と同じ。
package main

import (
	"os"

	"github.com/waya0125/recfriio/internal/cli"
)

func main() {
	os.Exit(cli.Main("recmtvu3s", cli.U3S, os.Args[1:]))
}
