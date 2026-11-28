#!/bin/bash
# peek.sh NAME [SPEC]: prints the screen of casts/NAME.cast at SPEC (a time in
# seconds, MARK or MARK+SECONDS; default: the latest frame). Use it to watch a
# running recording or to find rows and columns for steps and camera moves.
here="$(cd "$(dirname "$0")" && pwd)"
node "$here/../dump.mjs" "$here/../casts/$1.cast" "${2:-99999}" | grep -v -E '^ ?[0-9]+\|\s*$'
