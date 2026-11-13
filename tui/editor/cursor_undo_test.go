package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// Ported from VS Code's cursorUndo.test.ts; keys go through HandleEvent and
// Ctrl+U is the real binding.

func cursorUndoKey(e *Editor) { press(e, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}) }

func TestCursorUndoIssue82535EdgeCase(t *testing.T) {
	e := undoStopEditor(t, "")
	// type hello
	vsType(e, "hello")
	// press left
	press(e, tea.KeyPressMsg{Code: tea.KeyLeft})
	// press Delete
	press(e, tea.KeyPressMsg{Code: tea.KeyDelete})
	if e.Value() != "hell" {
		t.Fatalf("value %q", e.Value())
	}
	assertCursor(t, e, 1, 5)
	// press left
	press(e, tea.KeyPressMsg{Code: tea.KeyLeft})
	assertCursor(t, e, 1, 4)
	// press Ctrl+U
	cursorUndoKey(e)
	assertCursor(t, e, 1, 5)
}

func TestCursorUndoIssue82535EdgeCaseReverse(t *testing.T) {
	e := undoStopEditor(t, "")
	vsType(e, "hell")
	vsType(e, "o")
	if e.Value() != "hello" {
		t.Fatalf("value %q", e.Value())
	}
	assertCursor(t, e, 1, 6)
	cursorUndoKey(e)
	assertCursor(t, e, 1, 6)
}

// Caret moves are not document edits: Ctrl+U walks the moves back and Cursor
// Redo forward without changing text or document history.
func TestCursorUndoWalksCaretMovesWithoutEditing(t *testing.T) {
	e := undoStopEditor(t, "alpha\nbeta\ngamma")
	vsSetSelections(t, e, vsSelection{1, 1, 1, 1})
	press(e, tea.KeyPressMsg{Code: tea.KeyDown})
	press(e, tea.KeyPressMsg{Code: tea.KeyEnd})
	press(e, tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	revision := e.Revision()
	cursorUndoKey(e)
	assertCursor(t, e, 2, 5)
	cursorUndoKey(e)
	assertCursor(t, e, 2, 1)
	e.HandleEvent(ActionMsg{ID: "select.cursorRedo"})
	assertCursor(t, e, 2, 5)
	if e.Revision() != revision || e.Value() != "alpha\nbeta\ngamma" {
		t.Fatal("cursor undo changed the source")
	}
}

// VS Code's Add Cursors to Line Ends (Shift+Alt+I): every selected line gets a
// caret at its end; a selection ending at column 1 adds none on its last line.
func TestCaretsAtLineEndsFollowTheSelection(t *testing.T) {
	e := undoStopEditor(t, "one\ntwo2\nthree\nfour")
	vsSetSelections(t, e, vsSelection{1, 2, 3, 3})
	press(e, tea.KeyPressMsg{Code: 'i', Mod: tea.ModAlt | tea.ModShift})
	vsType(e, "!")
	if got := e.Value(); got != "one!\ntwo2!\nth!ree\nfour" {
		t.Fatalf("typing at line ends produced %q", got)
	}
	e = undoStopEditor(t, "one\ntwo\nthree")
	vsSetSelections(t, e, vsSelection{1, 1, 3, 1})
	e.HandleEvent(ActionMsg{ID: "select.caretsAtLineEnds"})
	vsType(e, ";")
	if got := e.Value(); got != "one;\ntwo;\nthree" {
		t.Fatalf("column-1 end produced %q", got)
	}
}

// The added VS Code chords resolve to the editor's existing actions.
func TestVSCodeEditorChordsResolve(t *testing.T) {
	for _, tc := range []struct {
		key  tea.KeyPressMsg
		want ui.ActionID
	}{
		{tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, "select.cursorUndo"},
		{tea.KeyPressMsg{Code: 'i', Mod: tea.ModAlt | tea.ModShift}, "select.caretsAtLineEnds"},
		{tea.KeyPressMsg{Code: 'g', Mod: tea.ModAlt | tea.ModShift}, "select.caretsAtLineEnds"},
		{tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl | tea.ModShift}, "select.allOccurrences"},
		{tea.KeyPressMsg{Code: tea.KeyF2, Mod: tea.ModCtrl}, "select.allOccurrences"},
		{tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl | tea.ModShift}, "move.matchingBracket"},
	} {
		got, ok := ui.MatchEditorAction(tc.key)
		if !ok || got.ID != tc.want {
			t.Errorf("%s resolved to %q (%v), want %q", tc.key.String(), got.ID, ok, tc.want)
		}
	}
}

// VS Code's scrollPageDown/scrollPageUp (Alt+PgDn/PgUp) move the view by one
// page and leave the caret and selection where they are.
func TestScrollPageMovesTheViewNotTheCaret(t *testing.T) {
	text := ""
	for range 200 {
		text += "line\n"
	}
	e := undoStopEditor(t, text)
	vsSetSelections(t, e, vsSelection{3, 2, 3, 2})
	page := e.bodyHeight()
	press(e, tea.KeyPressMsg{Code: tea.KeyPgDown, Mod: tea.ModAlt})
	if got := e.scrollRow(); got != page {
		t.Fatalf("Alt+PgDn scrolled to row %d, want %d", got, page)
	}
	assertCursor(t, e, 3, 2)
	press(e, tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModAlt})
	if got := e.scrollRow(); got != 0 {
		t.Fatalf("Alt+PgUp scrolled to row %d, want 0", got)
	}
	assertCursor(t, e, 3, 2)
}
