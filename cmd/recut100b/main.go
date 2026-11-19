// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

// recut100b は recfriio を QRS-UT100B に決めて動かす（前からある名前を残すためのもの）。
// recfriio --hdp と同じ。
package main

import (
	"os"

	"github.com/waya0125/recfriio/internal/cli"
)

func main() {
	os.Exit(cli.Main("recut100b", cli.UT100B, os.Args[1:]))
}
