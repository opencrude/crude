package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The "Undo stops" suite of VS Code's cursor.test.ts
// (src/vs/editor/test/browser/controller/cursor.test.ts), ported case by case
// with the same text, selections and expectations. Positions use VS Code's
// 1-based Selection(line, column, line, column); keys go through HandleEvent.
// "can undo typing and EOL change in one undo stop" is not ported: Crude has no
// pushEOL command.

type vsSelection struct{ startLine, startColumn, endLine, endColumn int }

func undoStopEditor(t *testing.T, text string) *Editor {
	t.Helper()
	return opsEditor(t, "sample.txt", text)
}

func vsSetSelections(t *testing.T, e *Editor, selections ...vsSelection) {
	t.Helper()
	spans := make([]selectionSpan, len(selections))
	for i, s := range selections {
		spans[i] = selectionSpan{SourceSelection: SourceSelection{
			Anchor: e.buf.positionToOffset(Position{Line: s.startLine - 1, Col: s.startColumn - 1}),
			Cursor: e.buf.positionToOffset(Position{Line: s.endLine - 1, Col: s.endColumn - 1}),
		}, primary: i == 0}
	}
	if err := e.setSelections(spans); err != nil {
		t.Fatal(err)
	}
	e.BreakUndoGroup() // VS Code's selection command pushes a stop
}

func vsType(e *Editor, text string) {
	for _, r := range text {
		press(e, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func vsRun(e *Editor, code rune, times int) {
	for range times {
		press(e, tea.KeyPressMsg{Code: code})
	}
}

func vsUndo(e *Editor) { press(e, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}) }

func assertLine(t *testing.T, e *Editor, line int, want string) {
	t.Helper()
	if got := e.LineText(line - 1); got != want {
		t.Fatalf("line %d = %q, want %q (value %q)", line, got, want, e.Value())
	}
}

func assertCursor(t *testing.T, e *Editor, line, column int) {
	t.Helper()
	if e.HasSelection() || e.cursor != (Position{Line: line - 1, Col: column - 1}) {
		t.Fatalf("cursor %+v selection %v, want Selection(%d, %d, %d, %d)", e.cursor, e.HasSelection(), line, column, line, column)
	}
}

func TestUndoStopsBetweenTypingAndDeleting(t *testing.T) {
	for _, tc := range []struct {
		name           string
		key            rune
		expectedLine   string
		expectedColumn int
	}{
		{"there is an undo stop between typing and deleting left", tea.KeyBackspace, "A fir line", 6},
		{"there is an undo stop between typing and deleting right", tea.KeyDelete, "A firstine", 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := undoStopEditor(t, "A  line\nAnother line")
			vsSetSelections(t, e, vsSelection{1, 3, 1, 3})
			vsType(e, "first")
			assertLine(t, e, 1, "A first line")
			assertCursor(t, e, 1, 8)

			vsRun(e, tc.key, 2)
			assertLine(t, e, 1, tc.expectedLine)
			assertCursor(t, e, 1, tc.expectedColumn)

			vsUndo(e)
			assertLine(t, e, 1, "A first line")
			assertCursor(t, e, 1, 8)

			vsUndo(e)
			assertLine(t, e, 1, "A  line")
			assertCursor(t, e, 1, 3)
		})
	}
}

func TestUndoStopBetweenDeletingLeftAndTyping(t *testing.T) {
	e := undoStopEditor(t, "A  line\nAnother line")
	vsSetSelections(t, e, vsSelection{2, 8, 2, 8})
	vsRun(e, tea.KeyBackspace, 7)
	assertLine(t, e, 2, " line")
	assertCursor(t, e, 2, 1)

	vsType(e, "Second")
	assertLine(t, e, 2, "Second line")
	assertCursor(t, e, 2, 7)

	vsUndo(e)
	assertLine(t, e, 2, " line")
	assertCursor(t, e, 2, 1)

	vsUndo(e)
	assertLine(t, e, 2, "Another line")
	assertCursor(t, e, 2, 8)
}

func TestUndoStopBetweenDeletingLeftAndDeletingRight(t *testing.T) {
	e := undoStopEditor(t, "A  line\nAnother line")
	vsSetSelections(t, e, vsSelection{2, 8, 2, 8})
	vsRun(e, tea.KeyBackspace, 7)
	assertLine(t, e, 2, " line")
	assertCursor(t, e, 2, 1)

	vsRun(e, tea.KeyDelete, 5)
	assertLine(t, e, 2, "")
	assertCursor(t, e, 2, 1)

	vsUndo(e)
	assertLine(t, e, 2, " line")
	assertCursor(t, e, 2, 1)

	vsUndo(e)
	assertLine(t, e, 2, "Another line")
	assertCursor(t, e, 2, 8)
}

func TestUndoStopBetweenDeletingRightAndTyping(t *testing.T) {
	e := undoStopEditor(t, "A  line\nAnother line")
	vsSetSelections(t, e, vsSelection{2, 9, 2, 9})
	vsRun(e, tea.KeyDelete, 4)
	assertLine(t, e, 2, "Another ")
	assertCursor(t, e, 2, 9)

	vsType(e, "text")
	assertLine(t, e, 2, "Another text")
	assertCursor(t, e, 2, 13)

	vsUndo(e)
	assertLine(t, e, 2, "Another ")
	assertCursor(t, e, 2, 9)

	vsUndo(e)
	assertLine(t, e, 2, "Another line")
	assertCursor(t, e, 2, 9)
}

func TestUndoStopBetweenDeletingRightAndDeletingLeft(t *testing.T) {
	e := undoStopEditor(t, "A  line\nAnother line")
	vsSetSelections(t, e, vsSelection{2, 9, 2, 9})
	vsRun(e, tea.KeyDelete, 4)
	assertLine(t, e, 2, "Another ")
	assertCursor(t, e, 2, 9)

	vsRun(e, tea.KeyBackspace, 6)
	assertLine(t, e, 2, "An")
	assertCursor(t, e, 2, 3)

	vsUndo(e)
	assertLine(t, e, 2, "Another ")
	assertCursor(t, e, 2, 9)

	vsUndo(e)
	assertLine(t, e, 2, "Another line")
	assertCursor(t, e, 2, 9)
}

func TestUndoStopInsertedWhenTypingSpace(t *testing.T) {
	e := undoStopEditor(t, "A  line\nAnother line")
	vsSetSelections(t, e, vsSelection{1, 3, 1, 3})
	vsType(e, "first and interesting")
	assertLine(t, e, 1, "A first and interesting line")
	assertCursor(t, e, 1, 24)

	vsUndo(e)
	assertLine(t, e, 1, "A first and line")
	assertCursor(t, e, 1, 12)

	vsUndo(e)
	assertLine(t, e, 1, "A first line")
	assertCursor(t, e, 1, 8)

	vsUndo(e)
	assertLine(t, e, 1, "A  line")
	assertCursor(t, e, 1, 3)
}

func TestUndoStopsIssue93585UndoMultiCursorEditCorruptsDocument(t *testing.T) {
	e := undoStopEditor(t, "hello world\nhello world")
	vsSetSelections(t, e, vsSelection{2, 7, 2, 12}, vsSelection{1, 7, 1, 12})
	vsType(e, "no")
	if got := e.Value(); got != "hello no\nhello no" {
		t.Fatalf("typed %q", got)
	}
	vsUndo(e)
	if got := e.Value(); got != "hello world\nhello world" {
		t.Fatalf("undo left %q", got)
	}
}

func TestUndoStopSingleForConsecutiveWhitespaces(t *testing.T) {
	e := undoStopEditor(t, "")
	vsType(e, "a")
	vsType(e, "b")
	vsType(e, " ")
	vsType(e, " ")
	vsType(e, "c")
	vsType(e, "d")
	for i, want := range []string{"ab  cd", "ab  ", "ab", ""} {
		if i > 0 {
			vsUndo(e)
		}
		if got := e.Value(); got != want {
			t.Fatalf("assert%d: %q, want %q", i+1, got, want)
		}
	}
}

func TestUndoStopNoneAfterASingleWhitespace(t *testing.T) {
	e := undoStopEditor(t, "")
	vsType(e, "a")
	vsType(e, "b")
	vsType(e, " ")
	vsType(e, "c")
	vsType(e, "d")
	for i, want := range []string{"ab cd", "ab", ""} {
		if i > 0 {
			vsUndo(e)
		}
		if got := e.Value(); got != want {
			t.Fatalf("assert%d: %q, want %q", i+1, got, want)
		}
	}
}

// VS Code restores an undo step's selections and reveals them with the least
// scrolling; it never moves the viewport back to where the edit was made.
func TestUndoRevealsTheCaretWithoutRestoringTheOldViewport(t *testing.T) {
	text := ""
	for i := range 100 {
		text += "line " + string(rune('a'+i%26)) + "\n"
	}
	e := undoStopEditor(t, text)
	e.SetCursorPosition(10, 0)
	vsType(e, "edited")
	wheel := func(times int) {
		for range times {
			_ = e.HandleEvent(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		}
	}
	wheel(2) // six rows; line 11 stays visible
	scrolled := e.scrollRow()
	if scrolled == 0 {
		t.Fatal("the wheel did not scroll")
	}
	vsUndo(e)
	if e.LineText(10) != "line k" || e.cursor != (Position{Line: 10}) {
		t.Fatalf("undo left %q at %+v", e.LineText(10), e.cursor)
	}
	if got := e.scrollRow(); got != scrolled {
		t.Fatalf("undo moved a visible caret's viewport from row %d to %d", scrolled, got)
	}
	press(e, tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	wheel(20) // the edited line is now far above the viewport
	vsUndo(e)
	if row := e.scrollRow(); row > 10 || 10 >= row+e.bodyHeight() {
		t.Fatalf("undo did not reveal line 11: viewport starts at row %d (height %d)", row, e.bodyHeight())
	}
}
