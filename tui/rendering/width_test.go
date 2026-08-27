package rendering

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestWidthMethodIsGrapheme(t *testing.T) {
	if WidthMethod != ansi.GraphemeWidth {
		t.Fatalf("WidthMethod = %v, want GraphemeWidth", WidthMethod)
	}
}

func TestEmojiGraphemeWidth(t *testing.T) {
	// Modern terminals (Kitty, Ghostty, WT grapheme mode): emoji cluster → 2.
	cases := []struct {
		s    string
		want int
	}{
		{"a", 1},
		{"中", 2},
		{"✨", 2},
		{"😀", 2},
		{"⚡", 2},
		{"🔧", 2},
		// ZWJ sequence is one cluster, width 2 (not N×2).
		{"🧑‍🌾", 2},
		{"👨‍💻", 2},
		{"🏳️‍🌈", 2},
	}
	for _, tc := range cases {
		got := PlainWidth(tc.s)
		if got != tc.want {
			t.Errorf("PlainWidth(%q) = %d, want %d", tc.s, got, tc.want)
		}
		if v := VisibleWidth(tc.s); v != tc.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", tc.s, v, tc.want)
		}
		if m := WidthMethod.StringWidth(tc.s); m != tc.want {
			t.Errorf("WidthMethod.StringWidth(%q) = %d, want %d", tc.s, m, tc.want)
		}
	}
}

func TestFirstGraphemeClusterEmoji(t *testing.T) {
	cluster, w := FirstGrapheme("🧑‍🌾 rest")
	if w != 2 {
		t.Fatalf("FirstGrapheme width = %d, want 2", w)
	}
	if PlainWidth(cluster) != 2 {
		t.Fatalf("cluster PlainWidth = %d, want 2", PlainWidth(cluster))
	}
}

func TestTrigramWidthStartsFromModernTerminalPolicy(t *testing.T) {
	for _, s := range []string{"☰", "\u2630", "\u2637"} {
		if got := PlainWidth(s); got != 2 {
			t.Errorf("PlainWidth(%q) = %d, want 2", s, got)
		}
	}
}

func TestDefaultModernWidthNeverExceedsTwo(t *testing.T) {
	// Before calibration, the modern grapheme guess caps at 2.
	samples := []string{"👨‍👩‍👧‍👦", "🏳️‍🌈", "🇯🇵", "中文", "a☰⚡😀"}
	for _, s := range samples {
		// Per-cluster
		rest := s
		for len(rest) > 0 {
			cluster, w := FirstGrapheme(rest)
			if w < 0 || w > 2 {
				t.Errorf("cluster %q width %d out of 0..2", cluster, w)
			}
			if len(cluster) == 0 {
				break
			}
			rest = rest[len(cluster):]
		}
	}
}

func TestLayoutMatchesANSIMethod(t *testing.T) {
	// Layout PlainWidth must equal what UV gets via ansi.GraphemeWidth
	// (same patched FirstGraphemeCluster).
	samples := []string{"☰ Interface", "⚡ Providers", "◆ Appearance", "👨‍💻 code", "中文"}
	for _, s := range samples {
		a := ansi.GraphemeWidth.StringWidth(s)
		b := PlainWidth(s)
		if a != b {
			t.Errorf("%q ansi=%d plain=%d", s, a, b)
		}
	}
}
