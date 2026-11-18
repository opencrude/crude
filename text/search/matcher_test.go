package textsearch

import (
	"math/rand"
	"reflect"
	"regexp"
	"testing"
	"unicode"
)

func TestLiteralSearchPreservesEverySimpleFoldPair(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		fold := unicode.SimpleFold(r)
		if fold == r {
			continue
		}
		matcher, err := Compile(string(fold), Options{})
		if err != nil {
			t.Fatal(err)
		}
		text := string(r)
		got := matcher.FindAll(text, 1)
		if len(got) != 1 || got[0].Start != 0 || got[0].End != len(text) {
			t.Fatalf("fold U+%04X/U+%04X: %+v", r, fold, got)
		}
	}
}

func TestLiteralSearchMatchesUnicodeRegexpSemantics(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	alphabet := []rune("aAkKsSİıKſé界 _-\n.")
	texts := []string{"İ foo KELVIN ſample", "FOO foo_food foo\nfoo", "é foo éfoo 界foo", "\xff foo \ufffd"}
	for range 100 {
		text := make([]rune, 80)
		for i := range text {
			text[i] = alphabet[rng.Intn(len(alphabet))]
		}
		texts = append(texts, string(text))
	}
	for _, query := range []string{"foo", "k", "s", "i", "a", " _", "\n", ".", "é", "界", "\ufffd"} {
		for _, caseSensitive := range []bool{false, true} {
			for _, wholeWord := range []bool{false, true} {
				opts := Options{CaseSensitive: caseSensitive, WholeWord: wholeWord}
				literal, err := Compile(query, opts)
				if err != nil {
					t.Fatal(err)
				}
				opts.Regex = true
				oracle, err := Compile(regexp.QuoteMeta(query), opts)
				if err != nil {
					t.Fatal(err)
				}
				for _, text := range texts {
					for _, limit := range []int{0, 1, 3} {
						got, want := literal.FindAll(text, limit), oracle.FindAll(text, limit)
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("%q in %q options=%+v limit=%d: got %+v, want %+v", query, text, opts, limit, got, want)
						}
					}
				}
			}
		}
	}
}

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
