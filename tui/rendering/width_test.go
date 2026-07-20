package rendering

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestActiveMethodIsGrapheme(t *testing.T) {
	// The testmain_test.go pin forces WidthProfileModern for this suite,
	// so ActiveMethod() must resolve to GraphemeWidth. Production code
	// follows the live profile (Modern ↔ Windows Terminal).
	if got := ansi.ActiveMethod(); got != ansi.GraphemeWidth {
		t.Fatalf("ActiveMethod must be GraphemeWidth under WidthProfileModern, got %v", got)
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
		if m := ansi.ActiveMethod().StringWidth(tc.s); m != tc.want {
			t.Errorf("ActiveMethod().StringWidth(%q) = %d, want %d", tc.s, m, tc.want)
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

func TestTrigramBurgerIsSingleCell(t *testing.T) {
	// U+2630 ☰: Unicode 16 tables say 2; Kitty/WT advance 1.
	for _, s := range []string{"☰", "\u2630", "\u2637"} {
		if got := PlainWidth(s); got != 1 {
			t.Errorf("PlainWidth(%q) = %d, want 1", s, got)
		}
	}
}

func TestWidthNeverExceedsTwo(t *testing.T) {
	// Even pathological clusters must cap at 2 (WT/Ghostty/foot).
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
