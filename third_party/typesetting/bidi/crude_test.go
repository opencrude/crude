// SPDX-License-Identifier: Unlicense OR BSD-3-Clause

package bidi

import "testing"

func TestLookupClassAndMirror(t *testing.T) {
	for r, want := range map[rune]Class{
		'a': L, 'א': R, 'ب': AL, '1': EN, '٣': AN, ' ': WS, '\t': S, '\n': B,
		'(': ON, '\u200f': R, '\u202e': RLO, '\u2067': RLI, '\u2069': PDI, '\u0300': NSM,
		'\u06f1': EN, // Persian digits are European numbers
	} {
		if got := LookupClass(r); got != want {
			t.Errorf("LookupClass(%U) = %d, want %d", r, got, want)
		}
	}
	if m, ok := Mirror('('); !ok || m != ')' {
		t.Errorf("Mirror('(') = %q, %v", m, ok)
	}
	if m, ok := Mirror('«'); !ok || m != '»' {
		t.Errorf("Mirror('«') = %q, %v", m, ok)
	}
	if _, ok := Mirror('a'); ok {
		t.Error("Mirror('a') reported a mirror")
	}
}

func TestParagraphLevel(t *testing.T) {
	var p Paragraph
	for _, c := range []struct {
		text string
		dir  Direction
		want Level
	}{
		{"abc", Neutral, 0}, {"سلام abc", Neutral, 1}, {"123", Neutral, 0},
		{"", RightToLeft, 1}, {"", Neutral, 0}, {"abc", RightToLeft, 1},
	} {
		p.SegmentString(c.text, c.dir)
		if got := p.ParagraphLevel(); got != c.want {
			t.Errorf("ParagraphLevel(%q, %d) = %d, want %d", c.text, c.dir, got, c.want)
		}
	}
}

func TestSegmentAutoFallback(t *testing.T) {
	var p Paragraph
	for _, c := range []struct {
		text     string
		fallback bool
		want     Level
	}{
		{"", true, 1}, {"123 - 4", true, 1}, {"123 - 4", false, 0}, {"abc", true, 0},
		{"سلام", false, 1}, {"\u2067abc\u2069", true, 1}, {"\u2066سلام\u2069 abc", true, 0},
	} {
		runs := p.SegmentAuto([]rune(c.text), c.fallback)
		if got := p.ParagraphLevel(); got != c.want {
			t.Errorf("SegmentAuto(%q, %v) level = %d, want %d", c.text, c.fallback, got, c.want)
		}
		if runs.NumRuns() > 0 && c.want == 1 && c.text == "123 - 4" {
			if r := runs.Run(0); r.Level != 2 {
				t.Errorf("numbers in an RTL paragraph: level %d, want 2", r.Level)
			}
		}
	}
	// A following Segment call must not inherit the fallback.
	p.Segment([]rune("123"), Neutral)
	if p.ParagraphLevel() != 0 {
		t.Fatal("Segment kept the previous SegmentAuto fallback")
	}
}
