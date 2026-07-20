package rendering

import (
	"github.com/charmbracelet/x/ansi"
)

// WidthMethod is the one display-width algorithm used by Crude layout,
// surface packing, and ultraviolet through Bubble Tea. See
// TERMINAL_WIDTH.md.
//
// Modern terminals, including Windows Terminal 1.22+, use:
//   - Grapheme clusters (UAX #29), not per-codepoint wcwidth
//   - Width ∈ {0, 1, 2} only (cap enforced by correctClusterWidth)
//   - Emoji / ZWJ / VS16 → 2
//   - VS15 text presentation → 1
//   - Unicode 16 wide symbols, including ☰, → 2
//
// Vendored x/ansi applies correctClusterWidth inside FirstGraphemeCluster so
// layout and UV always share one number. Never mix bare runewidth or uniseg
// for layout columns.
var WidthMethod = ansi.GraphemeWidth

// FirstGrapheme returns the first grapheme cluster and its cell width.
func FirstGrapheme(s string) (cluster string, width int) {
	return ansi.FirstGraphemeCluster(s, WidthMethod)
}

// PlainWidth is VisibleWidth for strings with no ANSI escapes.
func PlainWidth(s string) int {
	return WidthMethod.StringWidth(s)
}
