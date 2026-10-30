package syntax

import (
	"reflect"
	"testing"
	"unicode/utf8"
)

func FuzzIncrementalSyntax(f *testing.F) {
	f.Add("def answer():\n    return \"界\"\n", uint16(10), uint16(3), "🙂")
	f.Add("const id = async <T>(v: T): Promise<T> => v;", uint16(8), uint16(0), "entity")
	f.Add("\"\"\"\n# docstring\n\"\"\"\n", uint16(0), uint16(3), "")
	f.Fuzz(func(t *testing.T, source string, start, width uint16, insertion string) {
		if len(source)+len(insertion) > 4096 || !utf8.ValidString(source) || !utf8.ValidString(insertion) {
			t.Skip()
		}
		language := Python
		if len(source)%2 != 0 {
			language = TypeScript
		}
		runes := []rune(source)
		from := int(start) % (len(runes) + 1)
		to := from + int(width)%(len(runes)-from+1)
		current := string(runes[:from]) + insertion + string(runes[to:])
		s := newTestSession(t, language)
		parseTest(t, s, 1, source)
		got := parseTest(t, s, 2, current)
		fresh := newTestSession(t, language)
		want := parseTest(t, fresh, 1, current)
		if !reflect.DeepEqual(got.Spans, want.Spans) ||
			!reflect.DeepEqual(got.Problems, want.Problems) || got.HasError != want.HasError {
			t.Fatal("incremental result differs from a fresh document")
		}
	})
}
