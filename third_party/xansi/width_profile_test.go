package ansi

import (
	"testing"
)

// TestWindowsTerminalProfile verifies FirstGraphemeCluster / StringWidth
// against the empirical Windows Terminal advance measured via a CSI 6n
// cursor-position probe (see TERMINAL_WIDTH.md §"WT probe ground truth").
//
// WT without UAX 2027 (legacy console width model) advances per codepoint:
// zero-width codepoints (ZWJ, VS15, VS16, combining) each advance 1,
// regional indicators advance 2 each (no flag clustering), trigrams
// ☰ U+2630–U+2637 advance 1. There is no grapheme clustering at all.
func TestWindowsTerminalProfile(t *testing.T) {
	prev := SetWidthProfile(WidthProfileWindowsTerminal)
	defer SetWidthProfile(prev)

	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 0},
		{"ascii", "hello", 5},
		{"space", " ", 1},
		{"CJK", "中", 2},
		{"hiragana", "ひ", 2},
		{"hangul", "가", 2},
		{"fullwidth", "Ａ", 2},
		{"trigram burger", "☰", 1},
		{"trigram 7", "\u2637", 1},
		{"trigrams together", "☰\u2637", 2},

		// Single codepoints: zero-width-format chars advance 1 on WT.
		{"ZWJ alone", "\u200d", 1},
		{"VS16 alone", "\ufe0f", 1},
		{"VS15 alone", "\ufe0e", 1},
		{"combining grave alone", "\u0300", 1},
		// a + combining grave: 1 + 1 = 2 (no clustering).
		{"a + combining", "a\u0300", 2},

		// Text-default emoji and symbols.
		{"high voltage", "⚡", 2},
		{"recycle", "♻", 1},
		{"warning", "⚠", 1},
		{"heavy check", "✔", 1},
		{"star", "⭐", 2},
		{"white circle", "⚪", 2},
		{"black circle", "⚫", 2},
		{"hot beverage", "☕", 2},
		{"watch", "⌚", 2},
		{"hourglass", "⌛", 2},
		{"ballot box", "☐", 1},
		{"ballot check", "☑", 1},
		{"ballot x", "☒", 1},

		// Emoji-presentation defaults and SMP emoji.
		{"grinning", "😀", 2},
		{"check green", "✅", 2},
		{"sparkles", "✨", 2},
		{"cross mark", "❌", 2},
		{"question", "❓", 2},
		{"wrench", "🔧", 2},

		// VS16-qualified: base + 1 (the VS16 advances 1, not 0).
		{"star + VS16", "⭐️", 3},
		{"high voltage + VS16", "⚡️", 3},
		{"high voltage + VS15", "⚡︎", 3},
		{"recycle + VS16", "♻️", 2},
		{"warning + VS16", "⚠️", 2},
		{"heavy check + VS16", "✔️", 2},

		// ZWJ sequences: each emoji 2, each ZWJ 1, no clustering.
		// 🧑‍🌾 = 1F9D1 + 200D + 1F33E → 2 + 1 + 2 = 5
		{"zwj farmer", "🧑\u200d🌾", 5},
		// 👨‍💻 = 1F468 + 200D + 1F4BB → 2 + 1 + 2 = 5
		{"zwj technologist", "👨\u200d💻", 5},
		// 👨‍👩‍👧‍👦 = 4 emoji + 3 ZWJ → 8 + 3 = 11
		{"zwj family 4", "👨\u200d👩\u200d👧\u200d👦", 11},
		// 🏳️‍🌈 = 1F3F3 + FE0F + 200D + 1F308 → 2 + 1 + 1 + 2 = 6? Probe says 5.
		// WT renders 🏳 (1F3F3) as 1 column here (text-default white flag),
		// so: 1 + 1 + 1 + 2 = 5. wtRuneWidth(1F3F3) must return 1.
		{"rainbow flag", "🏳\ufe0f\u200d🌈", 5},

		// Regional indicator pairs: each 2, no flag clustering.
		// 🇯🇵 = 1F1EF + 1F1F5 → 2 + 2 = 4
		{"flag JP", "🇯🇵", 4},
		{"flag US", "🇺🇸", 4},

		// Box drawing / blocks / tree fallback icons.
		{"bullet", "•", 1},
		{"full block", "█", 1},
		{"light shade", "░", 1},
		{"box v", "│", 1},
		{"box h", "─", 1},
		{"box cross", "┼", 1},
		{"small tri right", "▸", 1},
		{"small tri down", "▾", 1},
		{"arrow NE", "↗", 1},

		// Mixed lines from the app.
		{"⚡ Providers", "⚡ Providers", 12},
		{"☰ Interface", "☰ Interface", 11},
		{"✅ DONE (partial)", "✅ DONE (partial)", 17},
	}

	for _, tc := range cases {
		got := StringWidth(tc.input)
		if got != tc.want {
			t.Errorf("StringWidth(%q) = %d, want %d (WT profile)", tc.input, got, tc.want)
		}
		// Method is irrelevant under WT profile; both must agree.
		if g2 := StringWidthWc(tc.input); g2 != tc.want {
			t.Errorf("StringWidthWc(%q) = %d, want %d (WT profile)", tc.input, g2, tc.want)
		}
		if g3 := GraphemeWidth.StringWidth(tc.input); g3 != tc.want {
			t.Errorf("GraphemeWidth.StringWidth(%q) = %d, want %d (WT profile)", tc.input, g3, tc.want)
		}
		if g4 := WcWidth.StringWidth(tc.input); g4 != tc.want {
			t.Errorf("WcWidth.StringWidth(%q) = %d, want %d (WT profile)", tc.input, g4, tc.want)
		}
	}
}

// TestWindowsTerminalFirstGrapheme asserts the slice+width of the first
// "cluster" (really: first codepoint) under the WT profile, including that
// ZWJ/VS16/combining are returned as standalone 1-cell units.
func TestWindowsTerminalFirstGrapheme(t *testing.T) {
	prev := SetWidthProfile(WidthProfileWindowsTerminal)
	defer SetWidthProfile(prev)

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
		{"\u200d", 3, 1},  // ZWJ alone → 1 cell, 3 bytes
		{"\ufe0f", 3, 1},  // VS16 alone → 1 cell, 3 bytes
		{"\ufe0e", 3, 1},  // VS15 alone → 1 cell, 3 bytes
		{"\u0300", 2, 1},  // combining grave → 1 cell, 2 bytes
		{"🇯🇵", 4, 2},     // first RI returns as 2-cell, 4 bytes
		{"🧑\u200d🌾", 4, 2}, // first emoji only, 4 bytes, 2 cells
	}
	for _, tc := range cases {
		seq, w := FirstGraphemeCluster(tc.in, GraphemeWidth)
		if w != tc.wantW {
			t.Errorf("FirstGraphemeCluster(%q) width = %d, want %d", tc.in, w, tc.wantW)
		}
		if len(seq) != tc.wantBytes {
			t.Errorf("FirstGraphemeCluster(%q) bytes = %d, want %d (got %q)", tc.in, len(seq), tc.wantBytes, seq)
		}
		// Method must not matter under WT profile.
		seq2, w2 := FirstGraphemeCluster(tc.in, WcWidth)
		if w2 != w || len(seq2) != len(seq) {
			t.Errorf("FirstGraphemeCluster(%q, WcWidth) differs from GraphemeWidth: (%q,%d) vs (%q,%d)", tc.in, seq2, w2, seq, w)
		}
	}
}

// TestWidthProfileAutoDetectFromEnv verifies the single decision function
// picks the WT profile when WT_SESSION is present in env and Modern
// otherwise. init() calls this with os.Environ(); Bubble Tea's renderer
// calls it with its (possibly injected) env.
func TestWidthProfileAutoDetectFromEnv(t *testing.T) {
	if got := DetectWidthProfileFromEnv(nil); got != WidthProfileModern {
		t.Errorf("DetectWidthProfileFromEnv(nil) = %v, want Modern", got)
	}
	if got := DetectWidthProfileFromEnv([]string{"TERM=xterm-256color"}); got != WidthProfileModern {
		t.Errorf("DetectWidthProfileFromEnv without WT_SESSION = %v, want Modern", got)
	}
	if got := DetectWidthProfileFromEnv([]string{"WT_SESSION=abc-123"}); got != WidthProfileWindowsTerminal {
		t.Errorf("DetectWidthProfileFromEnv with WT_SESSION=... = %v, want WindowsTerminal", got)
	}
	if got := DetectWidthProfileFromEnv([]string{"TERM=xterm-256color", "WT_SESSION="}); got != WidthProfileWindowsTerminal {
		t.Errorf("DetectWidthProfileFromEnv with empty WT_SESSION = %v, want WindowsTerminal (present-but-empty still counts)", got)
	}
}