package rendering

import (
	"strings"
	"testing"
)

func TestWordWrapPreservesWordAndGraphemeBoundaries(t *testing.T) {
	for _, test := range []struct {
		text  string
		width int
		want  []string
	}{
		{"one two three", 8, []string{"one two", "three"}},
		{"abc   def", 4, []string{"abc", "def"}},
		{"  alpha beta", 8, []string{"  alpha", "beta"}},
		{"abcdefghij", 3, []string{"abc", "def", "ghi", "j"}},
		{"你好世界", 4, []string{"你好", "世界"}},
		{"éé 👩‍💻!", 3, []string{"éé", "👩‍💻!"}},
		{"", 3, []string{""}},
	} {
		got := WordWrap(test.text, test.width)
		if strings.Join(got, "|") != strings.Join(test.want, "|") {
			t.Errorf("WordWrap(%q,%d) = %#v, want %#v", test.text, test.width, got, test.want)
		}
	}
}

func TestWordWrapVeryLongSourceHasNoMissingTail(t *testing.T) {
	source := strings.Repeat("word ", 40000) + "TAIL"
	rows := WordWrap(source, 31)
	if len(rows) < 1000 || rows[len(rows)-1] != "TAIL" && !strings.HasSuffix(rows[len(rows)-1], "TAIL") {
		t.Fatal("long source lost its final text")
	}
	if strings.Join(strings.Fields(strings.Join(rows, " ")), " ") != strings.Join(strings.Fields(source), " ") {
		t.Fatal("wrapping lost or duplicated source words")
	}
}

func TestFindSegmentStartsMatchesWordWrapBoundaries(t *testing.T) {
	cases := []struct {
		text  string
		width int
	}{
		{"", 3},
		{"one two three", 8},
		{"abc   def", 4},
		{"  alpha beta", 8},
		{"abcdefghij", 3},
		{"你好世界", 4},
		{"éé 👩‍💻!", 3},
		{strings.Repeat("word ", 400), 31},
		{strings.Repeat(" ", 40), 5},
		{"a\tb\tc", 3},
		{"trailing space ", 6},
		{"\x1b[31mred\x1b[0m text", 4},
		{"a\x1b[1mbold\x1b[0m c", 3},
	}
	// Deterministic pseudo-random word/space lines catch boundary differences
	// between the single-pass walk and WordWrap's own segment loop.
	seed := uint64(1)
	for i := 0; i < 200; i++ {
		var line strings.Builder
		for j := 0; j < 60; j++ {
			seed = seed*6364136223846793005 + 1442695040888963407
			switch seed % 5 {
			case 0:
				line.WriteString(" ")
			case 1:
				line.WriteString("ab")
			default:
				line.WriteString("word")
			}
		}
		cases = append(cases, struct {
			text  string
			width int
		}{line.String(), 1 + int(seed%20)})
	}

	for _, tc := range cases {
		runes := []rune(tc.text)
		segs := WordWrap(tc.text, tc.width)
		starts := FindSegmentStarts(runes, tc.width)
		if len(starts) != len(segs) {
			t.Fatalf("FindSegmentStarts(%q,%d) returned %d starts for %d segments",
				tc.text, tc.width, len(starts), len(segs))
		}
		for i, seg := range segs {
			segRunes := []rune(seg)
			start := starts[i]
			if start < 0 || start+len(segRunes) > len(runes) || string(runes[start:start+len(segRunes)]) != seg {
				t.Fatalf("segment %d of %q at width %d starts at %d, want the runes of %q",
					i, tc.text, tc.width, start, seg)
			}
		}
	}
}

func BenchmarkWordWrapLong(b *testing.B) {
	source := strings.Repeat("A very long question about deployment choices and safe defaults. ", 1024)
	for i := 0; i < b.N; i++ {
		_ = WordWrap(source, 47)
	}
}
