// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

package main

import (
	"io"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args    []string
		freq    int
		seconds int
		output  string
		b25     bool
	}{
		{[]string{"19", "60", "out.ts"}, 509, 60, "out.ts", false},
		{[]string{"--b25", "27", "-", "-"}, 557, 0, "-", true},
		{[]string{"-b25", "C13", "10", "x.ts"}, 111, 10, "x.ts", true},
		{[]string{"k63", "10", "x.ts"}, 465, 10, "x.ts", false},
	}
	for _, c := range cases {
		o, err := parseArgs(c.args, io.Discard)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if o.freqMHz != c.freq || o.seconds != c.seconds || o.output != c.output || o.b25 != c.b25 {
			t.Errorf("%v: %+v", c.args, o)
		}
	}
	for _, bad := range [][]string{{"12", "10", "x"}, {"19", "0", "x"}, {"19", "x"}, {"--pcsc", "19", "10", "x"}, {"B1", "10", "x"}} {
		if _, err := parseArgs(bad, io.Discard); err == nil {
			t.Errorf("%v が通った", bad)
		}
	}
}
