package editor

import (
	"testing"

	"crude/tui/theme"
)

func TestSearch_LiteralSubstring(t *testing.T) {
	th := theme.DefaultDark()
	ed := New(th)
	_ = ed.Open("") // no file; SetQuery uses internal buffer
	ed.buf.original = []byte("hello world hello")
	ed.buf.pieces = []piece{{buffer: originalBuffer, start: 0, length: len(ed.buf.original)}}
	ed.buf.invalidateCache()

	ed.SetQuery("hello")
	if got := len(ed.Search().Matches); got != 2 {
		t.Fatalf("want 2 matches, got %d", got)
	}
	if got := ed.Search().Matches[0].Start; got != (Position{Line: 0, Col: 0}) {
		t.Errorf("first match start = %v, want {0 0}", got)
	}
	if got := ed.Search().Matches[1].Start; got != (Position{Line: 0, Col: 12}) {
		t.Errorf("second match start = %v, want {0 12}", got)
	}
}

func TestSearch_CaseSensitive(t *testing.T) {
	th := theme.DefaultDark()
	ed := New(th)
	_ = ed.Open("")
	ed.buf.original = []byte("Hello hello HELLO")
	ed.buf.pieces = []piece{{buffer: originalBuffer, start: 0, length: len(ed.buf.original)}}
	ed.buf.invalidateCache()

	ed.SetQuery("hello")
	// case-insensitive (default): all three match
	if got := len(ed.Search().Matches); got != 3 {
		t.Fatalf("case-insensitive: want 3, got %d", got)
	}
	ed.SetOptions(SearchOptions{CaseSensitive: true})
	// case-sensitive: only the lowercase "hello" matches
	if got := len(ed.Search().Matches); got != 1 {
		t.Fatalf("case-sensitive: want 1, got %d", got)
	}
}

func TestSearch_WholeWord(t *testing.T) {
	th := theme.DefaultDark()
	ed := New(th)
	_ = ed.Open("")
	ed.buf.original = []byte("foo foobar foo")
	ed.buf.pieces = []piece{{buffer: originalBuffer, start: 0, length: len(ed.buf.original)}}
	ed.buf.invalidateCache()

	ed.SetQuery("foo")
	ed.SetOptions(SearchOptions{WholeWord: true})
	if got := len(ed.Search().Matches); got != 2 {
		t.Fatalf("whole word: want 2, got %d", got)
	}
}

func TestSearch_Regex(t *testing.T) {
	th := theme.DefaultDark()
	ed := New(th)
	_ = ed.Open("")
	ed.buf.original = []byte("a1 b22 c333 d4444")
	ed.buf.pieces = []piece{{buffer: originalBuffer, start: 0, length: len(ed.buf.original)}}
	ed.buf.invalidateCache()

	ed.SetQuery(`[a-z]\d+`)
	ed.SetOptions(SearchOptions{Regex: true})
	if got := len(ed.Search().Matches); got != 4 {
		t.Fatalf("regex: want 4, got %d", got)
	}
}

func TestSearch_NextPrevWraps(t *testing.T) {
	th := theme.DefaultDark()
	ed := New(th)
	_ = ed.Open("")
	ed.buf.original = []byte("x x x")
	ed.buf.pieces = []piece{{buffer: originalBuffer, start: 0, length: len(ed.buf.original)}}
	ed.buf.invalidateCache()

	ed.SetQuery("x")
	if got := len(ed.Search().Matches); got != 3 {
		t.Fatalf("want 3, got %d", got)
	}
	ed.Search().Next() // 0->1
	ed.Search().Next() // 1->2
	ed.Search().Next() // 2->0 (wrap)
	if got := ed.Search().CurrentIdx; got != 0 {
		t.Errorf("Next wrap: got idx %d, want 0", got)
	}
	ed.Search().Prev() // 0->2 (wrap backward)
	if got := ed.Search().CurrentIdx; got != 2 {
		t.Errorf("Prev wrap: got idx %d, want 2", got)
	}
}

func TestSearch_ReplaceCurrent(t *testing.T) {
	th := theme.DefaultDark()
	ed := New(th)
	_ = ed.Open("")
	ed.buf.original = []byte("foo bar foo")
	ed.buf.pieces = []piece{{buffer: originalBuffer, start: 0, length: len(ed.buf.original)}}
	ed.buf.invalidateCache()

	ed.SetQuery("foo")
	ed.SetReplace("BAZ")

	if got := len(ed.Search().Matches); got != 2 {
		t.Fatalf("want 2 matches, got %d", got)
	}
	ed.Search().CurrentIdx = 0
	ed.ReplaceCurrent()

	// After replacing the first "foo" with "BAZ", the buffer becomes
	// "BAZ bar foo" and the search re-runs against this new content.
	// We expect the single remaining "foo" to still be findable.
	want := "BAZ bar foo"
	if got := string(ed.buf.Bytes()); got != want {
		t.Errorf("buffer after ReplaceCurrent: got %q, want %q", got, want)
	}
	if !ed.Modified() {
		t.Errorf("editor should be modified after replace")
	}
	if got := len(ed.Search().Matches); got != 1 {
		t.Errorf("expected 1 remaining match, got %d", got)
	}
}

func TestSearch_ReplaceAll(t *testing.T) {
	th := theme.DefaultDark()
	ed := New(th)
	_ = ed.Open("")
	ed.buf.original = []byte("foo bar foo baz foo")
	ed.buf.pieces = []piece{{buffer: originalBuffer, start: 0, length: len(ed.buf.original)}}
	ed.buf.invalidateCache()

	ed.SetQuery("foo")
	ed.SetReplace("X")
	count := ed.ReplaceAll()
	if count != 3 {
		t.Errorf("ReplaceAll count: got %d, want 3", count)
	}
	want := "X bar X baz X"
	if got := string(ed.buf.Bytes()); got != want {
		t.Errorf("buffer: got %q, want %q", got, want)
	}
}

func TestSearch_MultilineMatches(t *testing.T) {
	th := theme.DefaultDark()
	ed := New(th)
	_ = ed.Open("")
	ed.buf.original = []byte("line one\nline two\nline one")
	ed.buf.pieces = []piece{{buffer: originalBuffer, start: 0, length: len(ed.buf.original)}}
	ed.buf.invalidateCache()

	ed.SetQuery("line one")
	if got := len(ed.Search().Matches); got != 2 {
		t.Fatalf("want 2, got %d", got)
	}
	if got := ed.Search().Matches[1].Start; got != (Position{Line: 2, Col: 0}) {
		t.Errorf("second match line: got %v, want {2 0}", got)
	}
}
