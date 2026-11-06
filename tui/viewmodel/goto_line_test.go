package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// goToLineApp opens a file of numbered marker lines and focuses its editor.
func goToLineApp(t *testing.T, lines int) *App {
	t.Helper()
	app, _ := newRealShellApp(t)
	content := make([]string, 0, lines)
	for i := 1; i <= lines; i++ {
		content = append(content, fmt.Sprintf("line %d marker", i))
	}
	path := filepath.Join(app.ws.RootPath(), "long.txt")
	if err := os.WriteFile(path, []byte(strings.Join(content, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	app.setFocusEditor()
	_ = app.View()
	if got := app.EditorState.Editor.LineCount(); got != lines {
		t.Fatalf("editor has %d lines, want %d", got, lines)
	}
	return app
}

func pressKeys(t *testing.T, app *App, keys ...tea.KeyPressMsg) {
	t.Helper()
	for _, key := range keys {
		_, cmd := app.Update(key)
		runAppCommand(t, app, cmd)
	}
}

func typeDigits(t *testing.T, app *App, digits string) {
	t.Helper()
	for _, r := range digits {
		pressKeys(t, app, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func ctrlG() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl} }

// Ctrl+G jumps to a line that is off screen, keeps the view context around it,
// and marks the line without selecting it, so typing does not replace it.
func TestGoToLineJumpsOffscreenTargetWithLanding(t *testing.T) {
	app := goToLineApp(t, 300)
	ed := app.EditorState.Editor
	text := ed.Value()
	if strings.Contains(frameText(app), "line 150 marker") {
		t.Fatal("precondition: line 150 is already on screen")
	}

	pressKeys(t, app, ctrlG())
	if app.TextPrompt == nil || app.windowManager.Window("text-prompt") == nil {
		t.Fatal("Ctrl+G did not open the go-to-line prompt")
	}
	if view := frameText(app); !strings.Contains(view, "Go to line") || !strings.Contains(view, "Line 1-300") {
		t.Fatalf("prompt does not state the accepted lines:\n%s", view)
	}
	// The current line is offered, already selected, so typing replaces it.
	if got := app.TextPrompt.ValueText(); got != "1" || !app.TextPrompt.Edit.HasSelection() {
		t.Fatalf("prefill %q selected=%v, want the current line selected", got, app.TextPrompt.Edit.HasSelection())
	}

	typeDigits(t, app, "150")
	pressKeys(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})

	if app.windowManager.Window("text-prompt") != nil {
		t.Fatal("the prompt stayed open after a valid line")
	}
	if ed.CursorLine() != 149 || ed.CursorCol() != 0 {
		t.Fatalf("cursor at line %d col %d, want 150:1", ed.CursorLine()+1, ed.CursorCol()+1)
	}
	if ed.HasSelection() {
		t.Fatal("the jump left an editable selection")
	}
	if ed.Value() != text {
		t.Fatal("the jump changed the document")
	}
	if !strings.Contains(frameText(app), "line 150 marker") {
		t.Fatal("the target line is not on screen")
	}
	if scroll := ed.ScrollY(); scroll == 0 || scroll >= 149 {
		t.Fatalf("landing has no context around it: scroll=%d", scroll)
	}

	// The landing is a cue, not a selection: typing inserts at the cursor.
	pressKeys(t, app, tea.KeyPressMsg{Code: 'X', Text: "X"})
	if got := ed.LineText(149); got != "Xline 150 marker" {
		t.Fatalf("typing did not insert at the landing: %q", got)
	}
}

// A target that is already visible does not move the view.
func TestGoToLineKeepsVisibleTargetInPlace(t *testing.T) {
	app := goToLineApp(t, 6)
	ed := app.EditorState.Editor
	pressKeys(t, app, ctrlG())
	typeDigits(t, app, "3")
	pressKeys(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	if ed.CursorLine() != 2 || ed.ScrollY() != 0 {
		t.Fatalf("visible target moved: line=%d scroll=%d", ed.CursorLine()+1, ed.ScrollY())
	}
}

// line:column lands on an exact column, and a column past the end of a line
// stops at its end.
func TestGoToLineAcceptsColumn(t *testing.T) {
	app := goToLineApp(t, 40)
	ed := app.EditorState.Editor
	pressKeys(t, app, ctrlG())
	typeDigits(t, app, "12:6")
	pressKeys(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	if ed.CursorLine() != 11 || ed.CursorCol() != 5 {
		t.Fatalf("cursor at line %d col %d, want 12:6", ed.CursorLine()+1, ed.CursorCol()+1)
	}

	pressKeys(t, app, ctrlG())
	typeDigits(t, app, "12:900")
	pressKeys(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	if ed.CursorLine() != 11 || ed.CursorCol() != len("line 12 marker") {
		t.Fatalf("past-the-end column at %d, want the line end %d", ed.CursorCol(), len("line 12 marker"))
	}
}

// Wrong input keeps the prompt open, states the cause in the dialog itself,
// and does not move the cursor. The first edit answers it.
func TestGoToLineRejectsBadInputWithoutMoving(t *testing.T) {
	app := goToLineApp(t, 12)
	ed := app.EditorState.Editor
	pressKeys(t, app, ctrlG())
	typeDigits(t, app, "abc")
	pressKeys(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.windowManager.Window("text-prompt") == nil {
		t.Fatal("the prompt closed on input that is not a line")
	}
	if view := frameText(app); !strings.Contains(view, "Enter a line number") {
		t.Fatalf("the dialog does not say what to enter:\n%s", view)
	}

	pressKeys(t, app, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if view := frameText(app); strings.Contains(view, "Enter a line number") || !strings.Contains(view, "Line 1-12") {
		t.Fatalf("the complaint outlived the first edit:\n%s", view)
	}

	app.TextPrompt.Edit.SelectAll()
	typeDigits(t, app, "99")
	pressKeys(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.windowManager.Window("text-prompt") == nil {
		t.Fatal("the prompt closed on a line the file does not have")
	}
	if view := frameText(app); !strings.Contains(view, "This file has 12 lines. Enter 1-12") {
		t.Fatalf("the dialog does not state the lines this file has:\n%s", view)
	}
	if ed.CursorLine() != 0 {
		t.Fatalf("rejected input moved the cursor to line %d", ed.CursorLine()+1)
	}

	pressKeys(t, app, tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.windowManager.Window("text-prompt") != nil {
		t.Fatal("Esc did not close the prompt")
	}
	if ed.CursorLine() != 0 {
		t.Fatal("Esc moved the cursor")
	}
}
