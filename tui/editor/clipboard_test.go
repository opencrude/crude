package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Cases ported from VS Code's cursor.test.ts with its default
// editor.emptySelectionClipboard. VS Code's viewModel.cut('keyboard') and
// paste(text, pasteOnNewLine) are driven here by the real keys; pasteOnNewLine
// comes from a preceding whole-line copy, as in the product.

// clipboardText returns the text a copy or cut key placed on the clipboard.
// The command itself writes the system clipboard and is not run in tests.
func clipboardText(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("no clipboard command")
	}
	clipboardLineCopy.Lock()
	defer clipboardLineCopy.Unlock()
	return clipboardLineCopy.text
}

// vsPaste is VS Code's viewModel.paste(text, pasteOnNewLine): the in-memory
// metadata says whether the text came from an empty-selection copy.
func vsPaste(e *Editor, text string, pasteOnNewLine bool) {
	rememberClipboardCopy(text, pasteOnNewLine)
	e.HandleEvent(tea.PasteMsg{Content: text})
}

func cutKey(e *Editor) tea.Cmd { return e.HandleEvent(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}) }

func TestClipboardBug16740CutLineCutsTheLastLine(t *testing.T) {
	// Part 1 => there is text on the last line
	e := undoStopEditor(t, "asdasd\nqwerty")
	vsSetSelections(t, e, vsSelection{2, 1, 2, 1})
	cutKey(e)
	if e.LineCount() != 1 || e.LineText(0) != "asdasd" {
		t.Fatalf("part 1: %q", e.Value())
	}

	// Part 2 => there is no text on the last line
	e = undoStopEditor(t, "asdasd\n")
	vsSetSelections(t, e, vsSelection{2, 1, 2, 1})
	cutKey(e)
	if e.LineCount() != 1 || e.LineText(0) != "asdasd" {
		t.Fatalf("part 2 first cut: %q", e.Value())
	}
	cutKey(e)
	if e.LineCount() != 1 || e.LineText(0) != "" {
		t.Fatalf("part 2 second cut: %q", e.Value())
	}
}

func TestClipboardIssue128602CuttingMultipleLinesErasesTheLastLine(t *testing.T) {
	e := undoStopEditor(t, "a1\na2\na3")
	vsSetSelections(t, e, vsSelection{1, 1, 1, 1}, vsSelection{2, 1, 2, 1}, vsSelection{3, 1, 3, 1})
	cutKey(e)
	if e.LineCount() != 1 || e.LineText(0) != "" {
		t.Fatalf("cut left %q", e.Value())
	}
}

func TestClipboardIssue85712PasteLineMovesCursorToStartOfNextLine(t *testing.T) {
	e := undoStopEditor(t, "abc123\n")
	vsSetSelections(t, e, vsSelection{2, 1, 2, 1})
	vsPaste(e, "something\n", true)
	if got := e.Value(); got != "abc123\nsomething\n" {
		t.Fatalf("paste produced %q", got)
	}
	assertCursor(t, e, 3, 1)
}

func TestClipboardIssue40695MaintainCursorPositionWhenCopyingLines(t *testing.T) {
	e := undoStopEditor(t, strings.Join([]string{
		"    function f() {",
		"        // I'm gonna copy this line",
		"        // Another line",
		"        return 3;",
		"    }",
	}, "\n"))
	vsSetSelections(t, e, vsSelection{4, 10, 4, 10})
	vsPaste(e, "        // I'm gonna copy this line\n", true)
	if got := e.Value(); got != strings.Join([]string{
		"    function f() {",
		"        // I'm gonna copy this line",
		"        // Another line",
		"        // I'm gonna copy this line",
		"        return 3;",
		"    }",
	}, "\n") {
		t.Fatalf("paste produced %q", got)
	}
	assertCursor(t, e, 5, 10)
}

// A copy from a selection is not a whole line: it pastes at the caret.
func TestClipboardSelectionCopyPastesAtTheCaret(t *testing.T) {
	e := undoStopEditor(t, "abc\nxyz")
	vsSetSelections(t, e, vsSelection{1, 1, 2, 1})
	text := clipboardText(t, e.HandleEvent(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}))
	vsSetSelections(t, e, vsSelection{2, 2, 2, 2})
	e.HandleEvent(tea.PasteMsg{Content: text})
	if got := e.Value(); got != "abc\nxabc\nyz" {
		t.Fatalf("paste produced %q", got)
	}
}

// getPlainTextToCopy: an empty selection copies its whole line with the line
// break; carets on one line copy it once; a selection copies its text.
func TestClipboardCopyWithEmptySelectionCopiesTheLine(t *testing.T) {
	e := undoStopEditor(t, "first\r\nsecond\r\nthird")
	vsSetSelections(t, e, vsSelection{2, 3, 2, 3})
	if got := clipboardText(t, e.HandleEvent(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})); got != "second\r\n" {
		t.Fatalf("copied %q", got)
	}
	vsSetSelections(t, e, vsSelection{1, 1, 1, 1}, vsSelection{1, 3, 1, 3}, vsSelection{3, 2, 3, 4})
	if got := clipboardText(t, e.HandleEvent(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})); got != "first\r\n\r\nhi" {
		t.Fatalf("multi-caret copy %q", got)
	}
	if e.Value() != "first\r\nsecond\r\nthird" {
		t.Fatal("copy changed the source")
	}
}

// The user's request: Shift+Delete removes the caret line as the CUA cut
// (VS Code on Windows, JetBrains everywhere). Ctrl+Insert and Shift+Insert are
// copy and paste; the line returns above the caret's line.
func TestClipboardCUAKeysCutCopyAndPasteLines(t *testing.T) {
	e := undoStopEditor(t, "one\ntwo\nthree\n")
	vsSetSelections(t, e, vsSelection{2, 2, 2, 2})
	text := clipboardText(t, e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDelete, Mod: tea.ModShift}))
	if text != "two\n" || e.Value() != "one\nthree\n" {
		t.Fatalf("Shift+Delete cut %q, left %q", text, e.Value())
	}
	assertCursor(t, e, 2, 1)
	vsUndo(e)
	if e.Value() != "one\ntwo\nthree\n" {
		t.Fatalf("undo after cut left %q", e.Value())
	}
	vsSetSelections(t, e, vsSelection{1, 1, 1, 1})
	if got := clipboardText(t, e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyInsert, Mod: tea.ModCtrl})); got != "one\n" {
		t.Fatalf("Ctrl+Insert copied %q", got)
	}
	if e.Value() != "one\ntwo\nthree\n" {
		t.Fatal("Ctrl+Insert changed the source")
	}
	if cmd := e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyInsert, Mod: tea.ModShift}); cmd == nil {
		t.Fatal("Shift+Insert did not read the clipboard")
	}
}
