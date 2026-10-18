package rendering

import (
	"slices"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestWidthMethodIsGrapheme(t *testing.T) {
	if WidthMethod != ansi.GraphemeWidth {
		t.Fatalf("WidthMethod = %v, want GraphemeWidth", WidthMethod)
	}
}

func TestTerminalWidthProbeCoversPresentationSensitiveClasses(t *testing.T) {
	corpus := TerminalWidthProbeCorpus()
	for _, cluster := range []string{
		"⏺", "⎿", "⏺︎", "⏺️", "▶", "▶️", "⚠︎", "⚠️", "#️⃣", "\uf17b",
	} {
		if !slices.Contains(corpus, cluster) {
			t.Errorf("terminal width probe lacks %q", cluster)
		}
	}
}

func TestTerminalWidthProbeCoversSourceTextFamilies(t *testing.T) {
	corpus := TerminalWidthProbeCorpus()
	families := map[string][]string{
		"pictured source":      {"e\u0301", "👩‍💻", "界"},
		"combining marks":      {"n\u0303", "a\u0308", "o\u0304", "a\u0323\u0301"},
		"CJK and fullwidth":    {"中", "文", "あ", "한", "Ａ"},
		"emoji bases":          {"😀", "🙂", "👩", "👨", "🧑", "💻"},
		"ZWJ professions":      {"👩‍💻", "👨‍💻", "🧑‍💻", "👩‍🔬", "👨‍🚀", "🧑‍⚕️"},
		"skin tones":           {"👍🏻", "👍🏼", "👍🏽", "👍🏾", "👍🏿", "👩🏽‍💻", "👨🏿‍🚀", "🧑🏻‍⚕️"},
		"families and couples": {"👨‍👩‍👧‍👦", "👩‍👩‍👦", "👨‍👨‍👧", "👩‍❤️‍👨", "👩‍❤️‍💋‍👩"},
		"flags":                {"🇺🇸", "🇨🇦", "🇺🇳", "🏴‍☠️", "🏳️‍⚧️", "🏴\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f"},
		"keycaps":              {"1️⃣", "9️⃣", "1⃣", "2⃣"},
		"presentation pairs":   {"❤", "❤️", "♥", "♥️", "☺", "☺️", "☀", "☀️", "✈", "✈️"},
		"Indic clusters":       {"क्ष", "कि", "நி"},
	}
	for family, samples := range families {
		t.Run(family, func(t *testing.T) {
			for _, cluster := range samples {
				if !slices.Contains(corpus, cluster) {
					t.Errorf("startup probe lacks %q (%U)", cluster, []rune(cluster))
				}
			}
		})
	}
}

func TestTerminalWidthProbeCorpusIsBoundedAndAtomic(t *testing.T) {
	corpus := TerminalWidthProbeCorpus()
	// This is a curated startup sample, not an exhaustive Unicode scan. Keep the
	// one-time request burst bounded; nothing is added to the render hot path.
	if len(corpus) > 208 {
		t.Fatalf("product probe grew to %d graphemes (budget 208)", len(corpus))
	}
	seen := make(map[string]bool, len(corpus))
	bytes := 0
	for _, sample := range corpus {
		cluster, _ := FirstGrapheme(sample)
		if sample == "" || cluster != sample || seen[sample] {
			t.Errorf("probe must be one distinct grapheme: %q", sample)
		}
		for _, r := range sample {
			if r < 32 || r >= 0x7f && r < 0xa0 {
				t.Errorf("probe contains terminal control: %q", sample)
			}
		}
		seen[sample] = true
		bytes += len(sample)
	}
	if bytes > 1536 {
		t.Fatalf("probe source bytes=%d exceed 1536-byte budget", bytes)
	}
}

func TestMeasuredRecordSymbolWidthOverridesEmojiGuess(t *testing.T) {
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{})
	t.Cleanup(func() { ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{}) })
	if got := PlainWidth("⏺"); got != 2 {
		t.Fatalf("unmeasured record symbol width=%d want Unicode emoji guess 2", got)
	}
	line := "│   ⏺ Edit(test/parser.test.js)"
	unmeasured := PlainWidth(line)
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"⏺": 1}})
	if got := PlainWidth(line); got != unmeasured-1 {
		t.Fatalf("measured cc-sample line width=%d want %d", got, unmeasured-1)
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
