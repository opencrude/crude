package ansi

import (
	"os"
	"strings"
)

// WidthProfile selects which terminal-behavior corrections the width path
// applies. The profile is the process-wide ground truth shared by Crude
// layout, Bubble Tea, and ultraviolet (all import this package), so the
// dual-source rule (layout_width == uv_cell_width == terminal_advance) is
// preserved on every platform.
//
// Background — terminals fall into two families:
//
//   - Modern / grapheme-aware (Kitty, Ghostty, WezTerm, foot, Alacritty,
//     Windows Terminal with UAX 2027 / Unicode Core mode negotiated ON):
//     grapheme clusters are real, every cluster advances at most 2 cells,
//     ZWJ/VS16/combining marks advance 0 inside a cluster.
//
//   - Windows Terminal before UAX 2027 (default conhost/legacy console width
//     model, mode 2027 not engaged): there is NO grapheme clustering. Every
//     codepoint advances on its own. ZWJ (U+200D), VS15 (U+FE0E), VS16
//     (U+FE0F), and combining marks each advance 1 column (not 0!), regional
//     indicators each advance 2 (no flag clustering), and a few symbols
//     Unicode 16 marks Wide (☰ U+2630–U+2637) still advance 1.
//
// Picking the wrong profile produces a multi-column drift on every emoji,
// ZWJ sequence, or VS16-qualified glyph, because layout/UV emit one
// (capped-at-2) cell while the terminal advances 3..N columns.
//
// The profile is auto-detected at init() from the WT_SESSION environment
// variable (set by Windows Terminal). Call [SetWidthProfile] to override,
// ideally before the first frame is rendered.
type WidthProfile int

const (
	// WidthProfileModern is the Kitty/Ghostty/foot/WezTerm/Alacritty
	// consensus (and Windows Terminal once ?2027 is negotiated ON):
	// grapheme clusters, cap 2, VS15→1, VS16/ZWJ/RI→2, trigrams ☰→1.
	WidthProfileModern WidthProfile = iota
	// WidthProfileWindowsTerminal is Windows Terminal with mode 2027 OFF:
	// per-codepoint measurement, zero-width codepoints (ZWJ/VS/combining)
	// advance 1, regional indicators advance 2 each (no flag clustering),
	// trigrams ☰→1. There is no grapheme clustering at all.
	WidthProfileWindowsTerminal
)

// activeWidthProfile is read on every FirstGraphemeCluster / DecodeSequence
// call, so changes via SetWidthProfile take effect immediately for both
// layout (Crude) and the renderer (UV via Bubble Tea).
var activeWidthProfile = DetectWidthProfileFromEnv(os.Environ())

// DetectWidthProfileFromEnv is the single decision function for the width
// profile. It is called once at init() with the process environment, and
// again by Bubble Tea's renderer with its (possibly injected) env so that
// tests with WithEnvironment(...) stay hermetic. Everyone else defers via
// [ActiveWidthProfile] / [ActiveMethod] — they must NOT re-branch on env.
func DetectWidthProfileFromEnv(env []string) WidthProfile {
	for _, kv := range env {
		if kv == "WT_SESSION" || strings.HasPrefix(kv, "WT_SESSION=") {
			return WidthProfileWindowsTerminal
		}
	}
	return WidthProfileModern
}

// SetWidthProfile overrides the auto-detected width profile process-wide.
// Both Crude layout and ultraviolet (via this package) read the same value,
// preserving the dual-source invariant. Call before the first frame is
// rendered. Returns the previous profile.
func SetWidthProfile(p WidthProfile) WidthProfile {
	prev := activeWidthProfile
	activeWidthProfile = p
	return prev
}

// ActiveWidthProfile returns the currently active width profile.
func ActiveWidthProfile() WidthProfile { return activeWidthProfile }

// MethodForProfile returns the cellbuf method that ultraviolet / layout should
// use for the given profile. Modern → GraphemeWidth (cluster + cap 2);
// WindowsTerminal → WcWidth (per-codepoint, no clustering). The method is
// what gets stored on uv.ScreenBuffer.Method and drives the
// DecodeSequence vs DecodeSequenceWc decoder choice in uv.printString.
//
// Under the WT profile xansi.FirstGraphemeCluster ignores the method
// (always per-codepoint), so the only practical effect of WcWidth here is
// to keep UV's decoder selection consistent and to suppress ?2027h
// emission in Bubble Tea's start()/close() (grapheme mode is meaningless
// without UAX 2027).
func MethodForProfile(p WidthProfile) Method {
	if p == WidthProfileWindowsTerminal {
		return WcWidth
	}
	return GraphemeWidth
}

// ActiveMethod returns MethodForProfile(ActiveWidthProfile()).
func ActiveMethod() Method { return MethodForProfile(activeWidthProfile) }