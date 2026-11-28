#!/bin/bash
# record.sh NAME: records casts/NAME.cast with tools/castrec (Windows, Git Bash).
#
# The steps come from record/steps/NAME.steps when it exists. Otherwise the
# recording starts Crude and waits; drive it by writing steps to
# casts/NAME.cast.inject and stop it by creating casts/NAME.done.
#
# Environment:
#   CRUDE_BIN    the crude executable to record (default: crude on PATH)
#   PROFILE_DIR  Crude data folder for the recording (CRUDE_HOME); created from
#                record/profile.json when it does not exist
#   PROJECT_DIR  folder the shell starts in (default: C:\dev\orbit, see orbit.sh)
#   COLS, ROWS   terminal size (default 160x45; the player expects 160x45)
# The recording gets its own copy of your Go settings (GOENV), so an agent in
# the recording cannot change the real ones.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
name="${1:?usage: record.sh NAME}"
work="${TMPDIR:-/tmp}/crude-demo-record"
mkdir -p "$work" "$here/../casts"

profile="${PROFILE_DIR:-$work/profile}"
if [ ! -d "$profile/.crude" ]; then
  mkdir -p "$profile/.crude"
  cp "$here/profile.json" "$profile/.crude/config.json"
fi
(cd "$root" && go build -o "$work/castrec.exe" ./tools/castrec)
if [ -f "$(go env GOENV)" ]; then cp "$(go env GOENV)" "$work/goenv"; else : > "$work/goenv"; fi

crude="${CRUDE_BIN:-$(command -v crude)}"
steps="$here/steps/$name.steps"
if [ ! -f "$steps" ]; then
  steps="$work/$name.steps"
  printf 'sleep:3\nmark:shell\ntypeslow:70:crude\nkey:Return\nmark:launch\nwaitfile:%s\nsleep:1\nmark:done\n' \
    "$(cygpath -w "$here/../casts/$name.done")" > "$steps"
fi

cd "$here/.."
env -u TERM -u TERM_PROGRAM COLORTERM=truecolor \
  GOENV="$(cygpath -w "$work/goenv")" CRUDE_HOME="$(cygpath -w "$profile")" PATH="$(dirname "$crude"):$PATH" \
  "$work/castrec.exe" -cols "${COLS:-160}" -rows "${ROWS:-45}" -cwd "${PROJECT_DIR:-C:\\dev\\orbit}" \
  -out "casts/$name.cast" -script "$(cygpath -w "$steps")" -- \
  pwsh -NoLogo -NoProfile -NoExit -File "$(cygpath -w "$here/shell.ps1")"
