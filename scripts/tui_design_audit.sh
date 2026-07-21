#!/usr/bin/env bash
# Phase 0 no-new-debt audit for the TUI design system.
# Fails if new hard-coded colors / forbidden width imports appear outside
# the frozen allowlist in docs/tui/bypass_manifest.json.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

fail=0

echo "== TUI design audit =="

# 1) Forbidden width libraries in tui/
if rg -n '"github.com/rivo/uniseg"' --glob '*.go' tui/ 2>/dev/null; then
  echo "FAIL: uniseg import in tui/"
  fail=1
else
  echo "OK: no uniseg imports in tui/"
fi

if rg -n '"github.com/mattn/go-runewidth"' --glob '*.go' tui/ 2>/dev/null; then
  echo "FAIL: runewidth import in tui/"
  fail=1
else
  echo "OK: no runewidth imports in tui/"
fi

# 2) go.mod replaces for terminal width pipeline
if ! grep -q 'replace charm.land/bubbletea/v2 => ./third_party/bubbletea' go.mod; then
  echo "FAIL: missing bubbletea replace"
  fail=1
else
  echo "OK: bubbletea replace present"
fi

if ! grep -q 'replace github.com/charmbracelet/x/ansi => ./third_party/xansi' go.mod; then
  echo "FAIL: missing xansi replace"
  fail=1
else
  echo "OK: xansi replace present"
fi

# 3) WidthMethod must stay GraphemeWidth
if ! rg -n 'WidthMethod\s*=\s*ansi\.GraphemeWidth' tui/rendering/width.go >/dev/null; then
  echo "FAIL: WidthMethod is not GraphemeWidth"
  fail=1
else
  echo "OK: WidthMethod = GraphemeWidth"
fi

# 4) Hard-coded lipgloss hex outside allowlist
# Allowlist directories/files from Phase 0 freeze
allow_hex=(
  'tui/theme/'
  'tui/scrollbar/style.go'
  'tui/diffview/'
  'tui/filetree/icons.go'
  'tui/filetree/render.go'
  'tui/widgets/statusbar.go'
  'tui/widgets/chatmessage.go'
  'tui/tools/'
  'tui/window/'
  'tui/xchroma/'
)

hex_hits="$(rg -n 'lipgloss\.Color\("#' --glob '*.go' --glob '!*_test.go' tui/ || true)"
if [[ -n "$hex_hits" ]]; then
  while IFS= read -r line; do
    path="${line%%:*}"
    allowed=0
    for a in "${allow_hex[@]}"; do
      case "$path" in
        $a*) allowed=1; break ;;
      esac
    done
    if [[ $allowed -eq 0 ]]; then
      echo "FAIL: new hard-coded lipgloss hex outside allowlist: $line"
      fail=1
    fi
  done <<< "$hex_hits"
  if [[ $fail -eq 0 ]]; then
    echo "OK: hard-coded hex only in allowlisted paths"
  fi
else
  echo "OK: no hard-coded lipgloss hex"
fi

# 5) Foundation packages must have tests
for pkg in tui/cell tui/surface tui/compositor tui/scrollbar; do
  if ! ls "$pkg"/*_test.go >/dev/null 2>&1; then
    echo "FAIL: $pkg has no tests"
    fail=1
  else
    echo "OK: $pkg has tests"
  fi
done

# 6) Manifest present
if [[ ! -f docs/tui/bypass_manifest.json ]]; then
  echo "FAIL: missing docs/tui/bypass_manifest.json"
  fail=1
else
  echo "OK: bypass manifest present"
fi

if [[ $fail -ne 0 ]]; then
  echo "== AUDIT FAILED =="
  exit 1
fi
echo "== AUDIT PASSED =="
