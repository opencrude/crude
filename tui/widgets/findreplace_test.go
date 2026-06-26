package widgets

import (
	"strings"
	"testing"

	"crude/tui/editor"
	"crude/tui/theme"
)

func TestFindReplaceLines_BasicFind(t *testing.T) {
	th := theme.DefaultDark()
	st := &FindReplaceState{
		ReplaceMode: false,
		FocusedField: 0,
		Query:       "hello",
		Cursor:      5,
		MatchCount:  3,
		CurrentIdx:  0,
	}
	lines := FindReplaceLines(th, st)
	if len(lines) == 0 {
		t.Fatal("no lines")
	}
	// First line is the top border.
	// Second line is the find input.
	if !strings.Contains(stripANSI(lines[1]), "he") {
		t.Errorf("find input missing query start: %q", stripANSI(lines[1]))
	}
	if !strings.Contains(stripANSI(lines[1]), "llo") {
		t.Errorf("find input missing query end: %q", stripANSI(lines[1]))
	}
	if !strings.Contains(stripANSI(lines[1]), "Find") {
		t.Errorf("find input missing label: %q", stripANSI(lines[1]))
	}
}

func TestFindReplaceLines_WithReplace(t *testing.T) {
	th := theme.DefaultDark()
	st := &FindReplaceState{
		ReplaceMode:  true,
		FocusedField: 1,
		Query:        "foo",
		Replace:      "bar",
		MatchCount:   2,
		CurrentIdx:   0,
	}
	lines := FindReplaceLines(th, st)
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}
	// Line 0: top border
	// Line 1: find field
	// Line 2: replace field
	plain := stripANSI(lines[2])
	if !strings.Contains(plain, "bar") {
		t.Errorf("replace line missing value: %q", plain)
	}
	if !strings.Contains(plain, "Replace") {
		t.Errorf("replace line missing label: %q", plain)
	}
}

func TestFindReplaceLines_TogglesRender(t *testing.T) {
	th := theme.DefaultDark()
	st := &FindReplaceState{
		ReplaceMode:    false,
		Query:          "x",
		CaseSensitive:  true,
		WholeWord:      true,
		Regex:          false,
		MatchCount:     1,
		CurrentIdx:     0,
	}
	lines := FindReplaceLines(th, st)
	all := strings.Join(lines, "\n")
	// New design uses pill-style toggles (" Aa ") not brackets ("[Aa]").
	if !strings.Contains(all, "Aa") {
		t.Errorf("case toggle not shown: %q", all)
	}
	if !strings.Contains(all, "\\b") {
		t.Errorf("word toggle not shown: %q", all)
	}
	if !strings.Contains(all, ".*") {
		t.Errorf("regex toggle not shown: %q", all)
	}
}

func TestFindReplaceLines_NoResults(t *testing.T) {
	th := theme.DefaultDark()
	st := &FindReplaceState{
		ReplaceMode: false,
		Query:       "nothere",
		MatchCount:  0,
		CurrentIdx:  -1,
	}
	lines := FindReplaceLines(th, st)
	all := strings.Join(lines, "\n")
	if !strings.Contains(all, "no results") {
		t.Errorf("expected 'no results' message: %q", all)
	}
}

func TestFindReplaceLines_EmptyQuery(t *testing.T) {
	th := theme.DefaultDark()
	st := &FindReplaceState{ReplaceMode: false, Query: ""}
	lines := FindReplaceLines(th, st)
	all := strings.Join(lines, "\n")
	if !strings.Contains(all, "type to search") {
		t.Errorf("expected 'type to search' message: %q", all)
	}
}

func TestFindReplaceFromEditor_PushAndPull(t *testing.T) {
	th := theme.DefaultDark()
	ed := editor.New(th)
	_ = ed.Open("")
	ed.SetQuery("hello")
	ed.SetReplace("world")
	ed.SetOptions(editor.SearchOptions{CaseSensitive: true, WholeWord: true})

	st := &FindReplaceState{}
	FindReplaceFromEditor(st, ed)

	if st.Query != "hello" {
		t.Errorf("Query: got %q, want %q", st.Query, "hello")
	}
	if st.Replace != "world" {
		t.Errorf("Replace: got %q, want %q", st.Replace, "world")
	}
	if !st.CaseSensitive {
		t.Errorf("CaseSensitive should be true")
	}
	if !st.WholeWord {
		t.Errorf("WholeWord should be true")
	}

	// Round-trip: change via popup, push back.
	st.Query = "goodbye"
	st.Replace = "farewell"
	FindReplaceToEditor(st, ed)
	if got := ed.Search().Query; got != "goodbye" {
		t.Errorf("editor query after push: got %q, want %q", got, "goodbye")
	}
	if got := ed.Search().Replace; got != "farewell" {
		t.Errorf("editor replace after push: got %q, want %q", got, "farewell")
	}
}

func stripANSI(s string) string {
	var out strings.Builder
	in := false
	for _, r := range s {
		if r == 0x1b {
			in = true
			continue
		}
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
