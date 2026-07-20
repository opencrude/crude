package rendering

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestWindowsTerminalProfile verifies the rendering package agrees with
// x/ansi and ultraviolet under the Windows Terminal per-codepoint profile
// (WT_SESSION set, UAX 2027 not negotiated ON). The WT advance was
// measured empirically with a CSI 6n cursor-position probe; see
// TERMINAL_WIDTH.md. Every entry here mirrors a probe result.
//
// These tests set the profile explicitly (no reliance on env), so they
// pass everywhere. Production code never branches — x/ansi is the single
// decision point.
func TestWindowsTerminalProfile(t *testing.T) {
	prev := ansi.SetWidthProfile(ansi.WidthProfileWindowsTerminal)
	defer ansi.SetWidthProfile(prev)

	cases := []struct {
		name  string
		input string
		want  int
	}{
		// Basics.
		{"empty", "", 0},
		{"ascii", "hello", 5},
		{"space", " ", 1},
		{"CJK", "中", 2},

		// Trigrams: WT actually advances 2 (wide glyph) on the modern
		// Windows Terminal despite the UAX#11 Neutral classification;
		// runewidth says 2 too, but we override to make the intent
		// explicit and protect against tables changing.
		{"trigram burger", "☰", 2},
		{"trigram 7", "\u2637", 2},

		// Zero-width codepoints each advance 1 (not 0) on WT.
		{"ZWJ alone", "\u200d", 1},
		{"VS16 alone", "\ufe0f", 1},
		{"VS15 alone", "\ufe0e", 1},
		{"combining alone", "\u0300", 1},
		// a + combining: 1 + 1 = 2 (no clustering).
		{"a + combining", "a\u0300", 2},

		// Single text-default emoji / symbols.
		{"high voltage", "⚡", 2},
		{"recycle", "♻", 1},
		{"warning", "⚠", 1},
		{"heavy check", "✔", 1},
		{"star", "⭐", 2},
		{"white circle", "⚪", 2},
		{"hot beverage", "☕", 2},
		{"ballot box", "☐", 1},
		{"ballot check", "☑", 1},

		// SMP emoji / EP-Yes.
		{"grinning", "😀", 2},
		{"check green", "✅", 2},
		{"sparkles", "✨", 2},
		{"cross mark", "❌", 2},
		{"wrench", "🔧", 2},

		// VS16-qualified: base width + 1 (VS16 advances 1).
		{"star + VS16", "⭐️", 3},
		{"high voltage + VS16", "⚡️", 3},
		{"recycle + VS16", "♻️", 2},
		{"warning + VS16", "⚠️", 2},
		{"heavy check + VS16", "✔️", 2},

		// ZWJ sequences: each emoji 2, each ZWJ 1, no clustering.
		{"zwj farmer", "🧑\u200d🌾", 5},
		{"zwj technologist", "👨\u200d💻", 5},
		{"zwj family 4", "👨\u200d👩\u200d👧\u200d👦", 11},
		{"rainbow flag", "🏳\ufe0f\u200d🌈", 5},

		// Regional indicators: 2 each, no flag clustering.
		{"flag JP", "🇯🇵", 4},
		{"flag US", "🇺🇸", 4},

		// Tree / chrome fallback glyphs.
		{"bullet", "•", 1},
		{"full block", "█", 1},
		{"box v", "│", 1},
		{"small tri right", "▸", 1},
		{"small tri down", "▾", 1},
		{"arrow NE", "↗", 1},

		// Real chrome lines from the app.
		{"⚡ Providers", "⚡ Providers", 12},
		{"☰ Interface", "☰ Interface", 11},
		{"✅ DONE (partial)", "✅ DONE (partial)", 17},
	}

	for _, tc := range cases {
		// VisibleWidth strips ANSI then measures; no ANSI here, so it
		// equals PlainWidth. Both must match the WT probe advance.
		if got := VisibleWidth(tc.input); got != tc.want {
			t.Errorf("%s: VisibleWidth(%q) = %d, want %d", tc.name, tc.input, got, tc.want)
		}
		if got := PlainWidth(tc.input); got != tc.want {
			t.Errorf("%s: PlainWidth(%q) = %d, want %d", tc.name, tc.input, got, tc.want)
		}
		// Layout and UV share x/ansi: the active method's StringWidth
		// must equal VisibleWidth, otherwise layout drifts from the
		// renderer regardless of the terminal.
		if got := ansi.ActiveMethod().StringWidth(tc.input); got != tc.want {
			t.Errorf("%s: ActiveMethod().StringWidth(%q) = %d, want %d", tc.name, tc.input, got, tc.want)
		}
	}
}

// TestFirstGraphemeWindowsTerminal asserts FirstGrapheme returns the first
// codepoint (not a cluster) under the WT profile, including that
// zero-width format characters come back as standalone 1-cell units.
func TestFirstGraphemeWindowsTerminal(t *testing.T) {
	prev := ansi.SetWidthProfile(ansi.WidthProfileWindowsTerminal)
	defer ansi.SetWidthProfile(prev)

	cases := []struct {
		in        string
		wantBytes int
		wantW     int
	}{
		{"a", 1, 1},
		{"中", 3, 2},
		{"☰", 3, 1},
		{"⚡", 3, 2},
		{"♻", 3, 1},
		{"⭐", 3, 2},
		{"😀", 4, 2},
		{"\u200d", 3, 1},  // ZWJ alone → 1 cell
		{"\ufe0f", 3, 1},  // VS16 alone → 1 cell
		{"\ufe0e", 3, 1},  // VS15 alone → 1 cell
		{"\u0300", 2, 1},  // combining → 1 cell
		{"🇯🇵", 4, 2},     // first RI: 2 cells, 4 bytes (no flag clustering)
		{"🧑\u200d🌾", 4, 2}, // first emoji only
	}
	for _, tc := range cases {
		cluster, w := FirstGrapheme(tc.in)
		if w != tc.wantW {
			t.Errorf("FirstGrapheme(%q) width = %d, want %d", tc.in, w, tc.wantW)
		}
		if len(cluster) != tc.wantBytes {
			t.Errorf("FirstGrapheme(%q) bytes = %d, want %d (got %q)", tc.in, len(cluster), tc.wantBytes, cluster)
		}
	}
}