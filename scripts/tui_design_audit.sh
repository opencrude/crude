#!/usr/bin/env bash
# Phase 0 no-new-debt audit for the TUI design system.
# Fails if new hard-coded colors / forbidden width imports appear outside
# the frozen allowlist in docs/reference/bypass-manifest.json.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

fail=0

echo "== TUI design audit =="

# Rendering and message coalescing consume neutral events, never protocol adapters.
if rg -n '"crude/provider(/[^\"]*)?"' --glob '*.go' --glob '!*_test.go' \
  tui/chatprojection tui/screens/shellchat tui/rendering tui/ui tui/tui.go types/reasoning types/accounting types/activity; then
  echo "FAIL: provider dependency in rendering or neutral reasoning contracts"
  fail=1
else
  echo "OK: rendering and reasoning contracts have no provider imports"
fi
if rg -n '"crude/(tui|workspace|session|provider)(/[^\"]*)?"' --glob '*.go' types/reasoning types/accounting types/activity; then
  echo "FAIL: neutral reasoning contract depends on a runtime layer"
  fail=1
else
  echo "OK: neutral reasoning contract is runtime-independent"
fi

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
)

hex_hits="$(rg -n 'lipgloss\.Color\("#' --glob '*.go' --glob '!*_test.go' tui/ || true)"
if [[ -n "$hex_hits" ]]; then
  while IFS= read -r line; do
    path="${line%%:*}"
    path="${path//\\//}" # normalize Windows separators for allowlist matching
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

# 4b) Raw truecolor / RGBA constructors outside theme (and tests).
# These bypass TokenSet and survive theme preset switches as fixed RGB strips.
allow_raw=(
  'tui/theme/'
  'tui/cell/'
  'tui/rendering/'
  'tui/term/'
)
raw_hits="$(rg -n 'NewTrueColor\(|color\.RGBA\{|cell\.Color\{[^}]*R:' --glob '*.go' --glob '!*_test.go' tui/ || true)"
if [[ -n "$raw_hits" ]]; then
  while IFS= read -r line; do
    path="${line%%:*}"
    path="${path//\\//}" # normalize Windows separators for allowlist matching
    allowed=0
    for a in "${allow_raw[@]}"; do
      case "$path" in
        $a*) allowed=1; break ;;
      esac
    done
    if [[ $allowed -eq 0 ]]; then
      echo "FAIL: raw colour constructor outside allowlist: $line"
      fail=1
    fi
  done <<< "$raw_hits"
  if [[ $fail -eq 0 ]]; then
    echo "OK: raw colour constructors only in allowlisted paths"
  fi
else
  echo "OK: no raw colour constructors outside allowlist"
fi

# 4c) Raw ANSI SGR outside the raw-ansi-sgr allowlist (manifest rule).
# Feature code must never emit raw \x1b[ sequences directly — it must paint
# through TokenSet/component cells or the rendering ANSI pipeline. This is
# the enforcement for the manifest's raw-ansi-sgr rule (previously
# declarative only; tui/ui/text_area.go raw \x1b[7m was caught by it).
allow_sgr=(
  'tui/cell/'
  'tui/compositor/'
  'tui/rendering/'
)
sgr_hits="$(rg -n '\\x1b\[' --glob '*.go' --glob '!*_test.go' tui/ || true)"
if [[ -n "$sgr_hits" ]]; then
  while IFS= read -r line; do
    path="${line%%:*}"
    path="${path//\\//}" # normalize Windows separators for allowlist matching
    allowed=0
    for a in "${allow_sgr[@]}"; do
      case "$path" in
        $a*) allowed=1; break ;;
      esac
    done
    if [[ $allowed -eq 0 ]]; then
      echo "FAIL: raw ANSI SGR outside allowlist: $line"
      fail=1
    fi
  done <<< "$sgr_hits"
  if [[ $fail -eq 0 ]]; then
    echo "OK: raw ANSI SGR only in allowlisted paths"
  fi
else
  echo "OK: no raw ANSI SGR outside allowlist"
fi

# 5) Foundation packages must have tests
for pkg in tui/cell tui/surface tui/compositor tui/ui; do
  if ! ls "$pkg"/*_test.go >/dev/null 2>&1; then
    echo "FAIL: $pkg has no tests"
    fail=1
  else
    echo "OK: $pkg has tests"
  fi
done

# 7) Legacy scrollbar package must stay deleted
if rg -n 'crude/tui/scrollbar' --glob '*.go' tui/ 2>/dev/null; then
  echo "FAIL: crude/tui/scrollbar import in tui/"
  fail=1
else
  echo "OK: no crude/tui/scrollbar imports"
fi
if [[ -d tui/scrollbar ]]; then
  echo "FAIL: tui/scrollbar directory still exists"
  fail=1
else
  echo "OK: tui/scrollbar package absent"
fi
if rg -n 'scrollbar_ansi' --glob '*.go' tui/ 2>/dev/null; then
  echo "FAIL: deleted scrollbar_ansi reference in tui/ Go sources"
  fail=1
else
  echo "OK: no scrollbar_ansi imports in tui/"
fi

# 7b) Legacy components package must stay deleted
if rg -n 'crude/tui/components' --glob '*.go' tui/ 2>/dev/null; then
  echo "FAIL: crude/tui/components import in tui/"
  fail=1
else
  echo "OK: no crude/tui/components imports"
fi
if [[ -d tui/components ]]; then
  echo "FAIL: tui/components directory still exists"
  fail=1
else
  echo "OK: tui/components package absent"
fi
if rg -n 'RenderFilterLine|FilterPaintOptions' --glob '*.go' tui/ 2>/dev/null; then
  echo "FAIL: deleted RenderFilterLine API still referenced in tui/"
  fail=1
else
  echo "OK: no RenderFilterLine references in tui/"
fi

# 8) subpane_render.go must stay native (no lipgloss/raw SGR)
if rg -n 'lipgloss|\\x1b\[' tui/viewmodel/subpane_render.go 2>/dev/null; then
  echo "FAIL: lipgloss/raw SGR in subpane_render.go"
  fail=1
else
  echo "OK: subpane_render.go native-only"
fi

# 9) deleted TabVertical APIs must stay gone
if rg -n 'TabVertical|VTabBarWidth|hitTestVertical' --glob '*.go' tui/ 2>/dev/null; then
  echo "FAIL: deleted vertical tab APIs still referenced in tui/"
  fail=1
else
  echo "OK: TabVertical residue absent"
fi

# 10) Manifest present
if [[ ! -f docs/reference/bypass-manifest.json ]]; then
  echo "FAIL: missing docs/reference/bypass-manifest.json"
  fail=1
else
  echo "OK: bypass manifest present"
fi

# 11) Three-layer boundary: workspace/ must never import crude/tui
# (Phase 14 exit criterion — dependency checks enforce the architecture).
if rg -n 'crude/tui' --glob '*.go' workspace/ 2>/dev/null; then
  echo "FAIL: workspace/ imports crude/tui (three-layer boundary violated)"
  fail=1
else
  echo "OK: workspace/ has no crude/tui imports"
fi

if [[ $fail -ne 0 ]]; then
  echo "== AUDIT FAILED =="
  exit 1
fi
echo "== AUDIT PASSED =="
