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

func BenchmarkWordWrapLong(b *testing.B) {
	source := strings.Repeat("A very long question about deployment choices and safe defaults. ", 1024)
	for i := 0; i < b.N; i++ {
		_ = WordWrap(source, 47)
	}
}
