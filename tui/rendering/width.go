package rendering

import (
	"github.com/charmbracelet/x/ansi"
)

// WidthMethod is the single process-wide display-width algorithm used by
// Crude layout, surface cell packing, and (via Bubble Tea) ultraviolet.
//
// Strategy (Kitty, Ghostty, WezTerm, Contour, foot, Windows Terminal grapheme mode):
// - Grapheme clusters (UAX #29), not per-codepoint wcwidth
// - Width ∈ {0, 1, 2} only (WT/Ghostty/foot cap)
// - Emoji / ZWJ / VS16 → 2
// - VS15 text presentation → 1
// - Symbol exceptions (☰ U+2630–2637) → 1 where tables say 2 but terminals advance 1
//
// Vendored x/ansi applies correctClusterWidth inside FirstGraphemeCluster so
// layout and UV always share one number. Never mix bare runewidth/uniseg.
var WidthMethod = ansi.GraphemeWidth

// FirstGrapheme returns the first grapheme cluster and its cell width
// under WidthMethod (includes ansi corrections).
func FirstGrapheme(s string) (cluster string, width int) {
	return ansi.FirstGraphemeCluster(s, WidthMethod)
}

// PlainWidth is VisibleWidth for strings with no ANSI escapes.
func PlainWidth(s string) int {
	return WidthMethod.StringWidth(s)
}
