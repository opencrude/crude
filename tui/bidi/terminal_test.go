package bidi

import "testing"

func TestTerminalTextDropsOnlyAppliedControls(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\ufeea\u200c", "\ufeea"},             // joined heh before a half-space
		{"\u0647\u200c", "\u0647"},             // nominal heh (Separate letters)
		{"\ufee0\u200d", "\ufee0"},             // ZWJ after a joined lam
		{"\u05d0\u200f", "\u05d0"},             // RLM attached to a Hebrew letter
		{"a\u200e", "a"},                       // LRM is never shown
		{"\u200f", " "},                        // a cell of controls keeps its column
		{"\u0628\u200c\u064e", "\u0628\u064e"}, // control inside the cluster
		{"👩\u200d💻", "👩\u200d💻"},               // emoji ZWJ sequence stays whole
		{"क\u094d\u200d", "क\u094d\u200d"},     // Indic half form stays whole
		{"x", "x"},
		{"\ufeb3", "\ufeb3"},
	}
	for _, c := range cases {
		if got := TerminalText(c.in); got != c.want {
			t.Errorf("TerminalText(%+q) = %+q, want %+q", c.in, got, c.want)
		}
	}
}

func TestTerminalTextDoesNotAllocateForSuffixControls(t *testing.T) {
	text := "\ufeea\u200c"
	if n := testing.AllocsPerRun(100, func() { _ = TerminalText(text) }); n != 0 {
		t.Fatalf("TerminalText allocates %.0f times per call", n)
	}
	plain := "\ufeea"
	if n := testing.AllocsPerRun(100, func() { _ = TerminalText(plain) }); n != 0 {
		t.Fatalf("TerminalText allocates %.0f times for a plain cell", n)
	}
}
