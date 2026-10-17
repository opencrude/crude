package editor

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// An editable document keeps a dragged selection after release so it can be
// typed over, indented or cut; release never writes the clipboard (it used to
// copy and clear, which threw the selection away before the edit).
func TestMouseDragReleaseKeepsEditableSelection(t *testing.T) {
	ed := newMotionTestEditor(t)
	textX := ed.gutterWidth()
	_ = ed.HandleEvent(tea.MouseClickMsg{X: textX, Y: 0, Button: tea.MouseLeft})
	_ = ed.HandleEvent(tea.MouseMotionMsg{X: textX + 4, Y: 0, Button: tea.MouseLeft})
	if cmd := ed.HandleEvent(tea.MouseReleaseMsg{X: textX + 4, Y: 0, Button: tea.MouseLeft}); cmd != nil {
		t.Fatalf("release of an editable drag returned a command (%T): it must not copy", cmd())
	}
	if got := ed.SelectedText(); got != "line" {
		t.Fatalf("selection after release = %q, want %q kept", got, "line")
	}
	if ed.IsMouseDragging() {
		t.Fatal("release left the drag armed")
	}

	_ = ed.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if got := ed.LineText(0); got != "X0" {
		t.Fatalf("typing over the kept selection = %q, want X0", got)
	}
}

// A read-only document is a reading surface: a drag copies on release and
// drops the selection, like the chat transcript.
func TestMouseDragReleaseCopiesReadOnlySelection(t *testing.T) {
	ed := newMotionTestEditor(t)
	ed.readOnly = true
	textX := ed.gutterWidth()
	_ = ed.HandleEvent(tea.MouseClickMsg{X: textX, Y: 0, Button: tea.MouseLeft})
	_ = ed.HandleEvent(tea.MouseMotionMsg{X: textX + 4, Y: 0, Button: tea.MouseLeft})
	if cmd := ed.HandleEvent(tea.MouseReleaseMsg{X: textX + 4, Y: 0, Button: tea.MouseLeft}); cmd == nil {
		t.Fatal("read-only drag release returned no copy command")
	}
	if ed.HasSelection() {
		t.Fatal("read-only drag release kept the selection")
	}
}

// Esc dismisses a selection before it means Back (closing the tab).
func TestEscClearsSelectionBeforeClosing(t *testing.T) {
	ed := newMotionTestEditor(t)
	ed.SetSelection(0, 0, 0, 4)
	if cmd := ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEsc}); cmd != nil {
		t.Fatalf("Esc with a selection returned %T; it must only drop the selection", cmd())
	}
	if ed.HasSelection() {
		t.Fatal("Esc kept the selection")
	}
	cmd := ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("second Esc did not request close")
	}
	if _, ok := cmd().(EditorCloseMsg); !ok {
		t.Fatal("second Esc is not the close request")
	}
}

// Double-click selects the word, triple-click the line, and Shift+click
// extends the selection from its anchor: the editor's pointer selection now
// persists, so it needs the standard ways to make one.
func TestMultiClickAndShiftClickSelection(t *testing.T) {
	ed := newMotionTestEditor(t)
	ed.ReplaceDocument("alpha beta(gamma)\nsecond line\n")
	textX := ed.gutterWidth()
	click := func(col int, mod tea.KeyMod) {
		_ = ed.HandleEvent(tea.MouseClickMsg{X: textX + col, Y: 0, Button: tea.MouseLeft, Mod: mod})
		_ = ed.HandleEvent(tea.MouseReleaseMsg{X: textX + col, Y: 0, Button: tea.MouseLeft})
	}
	click(7, 0)
	click(7, 0)
	if got := ed.SelectedText(); got != "beta" {
		t.Fatalf("double-click selected %q, want beta", got)
	}
	click(7, 0)
	if got := ed.SelectedText(); got != "alpha beta(gamma)\n" {
		t.Fatalf("triple-click selected %q, want the whole line", got)
	}

	ed.ClearSelection()
	ed.lastClickAt = time.Time{}
	click(2, 0)
	ed.lastClickAt = time.Time{}
	click(9, tea.ModShift)
	if got := ed.SelectedText(); got != "pha bet" {
		t.Fatalf("shift+click selected %q, want %q", got, "pha bet")
	}
}

// Typing over a selection replaces it as one undo step.
func TestTypingOverSelectionUndoesAsOneStep(t *testing.T) {
	ed := newMotionTestEditor(t)
	ed.ReplaceDocument("hello world\n")
	ed.SetSelection(0, 0, 0, 5)
	_ = ed.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if got := ed.LineText(0); got != "X world" {
		t.Fatalf("typed over selection = %q", got)
	}
	ed.Undo()
	if got := ed.LineText(0); got != "hello world" {
		t.Fatalf("one undo after replacing a selection = %q, want the original", got)
	}
	ed.SetSelection(0, 6, 0, 11)
	_ = ed.HandleEvent(tea.PasteMsg{Content: "there"})
	if got := ed.LineText(0); got != "hello there" {
		t.Fatalf("paste over selection = %q", got)
	}
}
