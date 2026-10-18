package textsearch

import "testing"

func TestExactSourceMatches(t *testing.T) {
	for _, tc := range []struct {
		query, text string
		opts        Options
		want        [][2]int
	}{
		{"foo", "foo foo", Options{}, [][2]int{{0, 3}, {4, 7}}},
		{" foo ", "foo  foo x", Options{}, [][2]int{{4, 9}}},
		{"kelvin", "KELVIN kelvin", Options{}, [][2]int{{0, 8}, {9, 15}}},
		{"x", "é xé éx x", Options{WholeWord: true}, [][2]int{{12, 13}}},
		{"^", "ab\ncd", Options{Regex: true}, [][2]int{{0, 0}, {3, 3}}},
	} {
		m, err := Compile(tc.query, tc.opts)
		if err != nil {
			t.Fatal(err)
		}
		got := m.FindAll(tc.text, 0)
		if len(got) != len(tc.want) {
			t.Fatalf("%q in %q: %+v", tc.query, tc.text, got)
		}
		for i, w := range tc.want {
			if got[i].Start != w[0] || got[i].End != w[1] {
				t.Fatalf("%q match %d: %+v want %v", tc.query, i, got[i], w)
			}
		}
	}
}

func TestRegexExpansionUsesOriginalContext(t *testing.T) {
	m, err := Compile(`(?P<word>foo)(\d+)`, Options{Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	text := "foo12 foo34"
	hits := m.FindAll(text, 0)
	if len(hits) != 2 {
		t.Fatal(hits)
	}
	if got := m.Expand(text, hits[1], `${word}:$2:$$`); got != "foo:34:$" {
		t.Fatal(got)
	}
	anchor, _ := Compile(`^`, Options{Regex: true})
	hits = anchor.FindAll("ab\ncd", 0)
	if got := anchor.Expand("ab\ncd", hits[1], "> "); got != "> " {
		t.Fatal(got)
	}
	literal, _ := Compile("foo", Options{})
	if got := literal.Expand("foo", literal.FindAll("foo", 1)[0], "$1"); got != "$1" {
		t.Fatal(got)
	}
	if _, err := Compile("[", Options{Regex: true}); err == nil {
		t.Fatal("invalid regex accepted")
	}
}
