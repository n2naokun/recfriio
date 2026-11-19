#!/bin/sh
# 同じ入力（B25 を通す前の TS）を、arib25（C）と recut100b の b25（Go）で外して比べる。
#   tools/arib25-compare/compare.sh <arib25 のソースの置き場> <card.log> <raw.ts> [Go の実機の出力]
# arib25 のソースは recfriio（github.com/waya0125/recfriio）の arib25/ など。
# card.log と raw.ts は recut100b --b25 --card-log card.log --raw raw.ts で作る（card.log は人に渡さない）。
set -eu
ARIB25=$(cd "$1" && pwd); LOG=$(realpath "$2"); RAW=$(realpath "$3"); LIVE=${4:+$(realpath "$4")}
HERE=$(cd "$(dirname "$0")" && pwd); ROOT=$(cd "$HERE/../.." && pwd)
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
cp "$ARIB25"/arib_std_b25.c "$ARIB25"/ts_section_parser.c "$ARIB25"/multi2.c "$ARIB25"/*.h "$WORK"/
cp "$HERE/replay.c" "$WORK"/
docker run --rm -u "$(id -u):$(id -g)" -v "$WORK:/w" -w /w golang:1.26-bookworm \
    gcc -O2 -o replay replay.c arib_std_b25.c ts_section_parser.c multi2.c
"$WORK/replay" "$LOG" "$RAW" "$WORK/c.ts"
"$ROOT/scripts/go.sh" build -o bin/goreplay ./tools/arib25-compare/goreplay
"$ROOT/bin/goreplay" "$LOG" "$RAW" "$WORK/go.ts"
sha256sum "$WORK/c.ts" "$WORK/go.ts" ${LIVE:+"$LIVE"} | sed "s|$WORK/||"
cmp "$WORK/c.ts" "$WORK/go.ts" && echo "arib25 と Go: 同じ"
[ -z "$LIVE" ] || { cmp "$WORK/go.ts" "$LIVE" && echo "Go（記録から）と Go（実機）: 同じ"; }
