package rendering

import (
	"github.com/charmbracelet/x/ansi"
)

// Width resolution is driven by ansi.ActiveMethod(), which honours the
// process-wide WidthProfile (Modern vs Windows Terminal). See
// third_party/xansi/width_profile.go and TERMINAL_WIDTH.md.
//
// Modern (Kitty, Ghostty, WezTerm, Contour, foot, Windows Terminal with
// UAX 2027 negotiated ON):
//   - Grapheme clusters (UAX #29), not per-codepoint wcwidth
//   - Width ∈ {0, 1, 2} only (cap enforced by correctClusterWidth)
//   - Emoji / ZWJ / VS16 → 2
//   - VS15 text presentation → 1
//   - Symbol exceptions (☰ U+2630–2637) → 1 where tables say 2 but
//     terminals advance 1
//
// Windows Terminal (WT_SESSION set, UAX 2027 OFF):
//   - Per-codepoint, no grapheme clustering
//   - ZWJ / VS15 / VS16 / combining marks each advance 1
//   - Regional indicators advance 2 each (no flag clustering)
//   - Aggregated sequences match: 🧑‍🌾=5, 🇯🇵=4, ♻️=2, ⚡️=3
//
// Vendored x/ansi applies correctClusterWidth inside FirstGraphemeCluster
// so layout and UV always share one number. Never mix bare
// runewidth/uniseg for layout columns.

// activeMethod is the ansi method routed through the active width profile.
// It is read on every call so SetWidthProfile (and the env-driven auto
// detect at init) take effect immediately for both Crude layout and
// ultraviolet (via the same package). Keeping it as a function — not a
// cached value — is the whole point: the method follows the profile.
func activeMethod() ansi.Method { return ansi.ActiveMethod() }

// FirstGrapheme returns the first grapheme cluster (modern) or first
// codepoint (WT) and its cell width under the active profile.
func FirstGrapheme(s string) (cluster string, width int) {
	return ansi.FirstGraphemeCluster(s, activeMethod())
}

// PlainWidth is VisibleWidth for strings with no ANSI escapes.
func PlainWidth(s string) int {
	return activeMethod().StringWidth(s)
}
