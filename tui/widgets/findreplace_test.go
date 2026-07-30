package widgets

import (
	"testing"

	"crude/tui/editor"
	"crude/tui/theme"
)

func TestFindReplaceFromEditor_PushAndPull(t *testing.T) {
	th := theme.DefaultDark()
	ed := editor.New(th)
	_ = ed.Open("")
	ed.SetQuery("hello")
	ed.SetReplace("world")
	ed.SetOptions(editor.SearchOptions{CaseSensitive: true, WholeWord: true})

	st := &FindReplaceState{}
	FindReplaceFromEditor(st, ed)

	if st.Query() != "hello" {
		t.Errorf("Query: got %q, want %q", st.Query(), "hello")
	}
	if st.Replace() != "world" {
		t.Errorf("Replace: got %q, want %q", st.Replace(), "world")
	}
	if !st.CaseSensitive {
		t.Errorf("CaseSensitive should be true")
	}
	if !st.WholeWord {
		t.Errorf("WholeWord should be true")
	}

	// Round-trip: change via popup, push back.
	st.SetQuery("goodbye")
	st.SetReplace("farewell")
	FindReplaceToEditor(st, ed)
	if got := ed.Search().Query; got != "goodbye" {
		t.Errorf("editor query after push: got %q, want %q", got, "goodbye")
	}
	if got := ed.Search().Replace; got != "farewell" {
		t.Errorf("editor replace after push: got %q, want %q", got, "farewell")
	}
}

// TestFindReplace_RuneSafeCursor verifies that TextEdit-backed
// find/replace correctly handles multi-byte input (CJK, emoji)
// without panicking or corrupting the cursor.
func TestFindReplace_RuneSafeCursor(t *testing.T) {
	_ = theme.DefaultDark()
	st := &FindReplaceState{}
	st.SetQuery("日本語")
	if got := st.FindEdit().Cursor(); got != 3 {
		t.Errorf("expected cursor at rune 3 after typing 3 CJK chars, got %d", got)
	}
	if st.Query() != "日本語" {
		t.Errorf("Query corrupted: got %q, want %q", st.Query(), "日本語")
	}
}
