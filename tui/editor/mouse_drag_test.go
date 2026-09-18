package editor

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

func newMotionTestEditor(t *testing.T) *Editor {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "lines.txt")
	content := "line0\nline1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	ed.viewW = 40
	ed.viewH = 20
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	return ed
}

func selectionSnapshot(ed *Editor) (sLine, sCol, eLine, eCol int, has bool) {
	sLine, sCol, eLine, eCol = ed.SelectionRange()
	return sLine, sCol, eLine, eCol, ed.HasSelection()
}

func TestKeyboardSelection_BareMouseMotionDoesNotExtend(t *testing.T) {
	ed := newMotionTestEditor(t)

	// Shift+Down × 3 from (0,0) → selection (0,0)-(3,0)
	for i := 0; i < 3; i++ {
		_ = ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	}
	sLine, sCol, eLine, eCol, has := selectionSnapshot(ed)
	if !has || sLine != 0 || sCol != 0 || eLine != 3 || eCol != 0 {
		t.Fatalf("after Shift+Down×3 want sel (0,0)-(3,0) has=%v got (%d,%d)-(%d,%d)",
			has, sLine, sCol, eLine, eCol)
	}
	if ed.IsMouseDragging() {
		t.Fatal("keyboard selection must not arm mouseDragging")
	}

	// Bare motion (no button) to a different row — must not change selection.
	_ = ed.HandleEvent(tea.MouseMotionMsg{X: 5, Y: 8, Button: 0})
	nsLine, nsCol, neLine, neCol, nhas := selectionSnapshot(ed)
	if !nhas || nsLine != sLine || nsCol != sCol || neLine != eLine || neCol != eCol {
		t.Fatalf("bare MouseMotion mutated keyboard selection: before (%d,%d)-(%d,%d) after (%d,%d)-(%d,%d)",
			sLine, sCol, eLine, eCol, nsLine, nsCol, neLine, neCol)
	}
}

func TestMousePressDragRelease_ExtendsSelection(t *testing.T) {
	ed := newMotionTestEditor(t)

	textX := ed.gutterWidth() + 2
	_ = ed.HandleEvent(tea.MouseClickMsg{X: textX, Y: 0, Button: tea.MouseLeft})
	if !ed.IsMouseDragging() || !ed.IsSelecting() {
		t.Fatal("left click should start mouseDragging + selecting")
	}

	_ = ed.HandleEvent(tea.MouseMotionMsg{X: textX, Y: 4, Button: tea.MouseLeft})
	_, _, eLine, _, has := selectionSnapshot(ed)
	if !has || eLine != 4 {
		t.Fatalf("drag motion should extend selEnd.Line to 4, got eLine=%d has=%v range=%v",
			eLine, has, func() any { a, b, c, d := ed.SelectionRange(); return [4]int{a, b, c, d} }())
	}
	if !ed.IsMouseDragging() {
		t.Fatal("mouseDragging should stay armed during drag")
	}

	_ = ed.HandleEvent(tea.MouseReleaseMsg{X: textX, Y: 4, Button: tea.MouseLeft})
	if ed.IsMouseDragging() {
		t.Fatal("release should clear mouseDragging")
	}
}

func TestCancelPointerCapture_ClearsMouseDragKeepsKeyboardSelection(t *testing.T) {
	ed := newMotionTestEditor(t)

	// Keyboard selection survives CancelPointerCapture (no mouse drag).
	for i := 0; i < 2; i++ {
		_ = ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	}
	sLine, sCol, eLine, eCol, has := selectionSnapshot(ed)
	if !has {
		t.Fatal("expected keyboard selection")
	}
	ed.CancelPointerCapture()
	nsLine, nsCol, neLine, neCol, nhas := selectionSnapshot(ed)
	if !nhas || nsLine != sLine || nsCol != sCol || neLine != eLine || neCol != eCol {
		t.Fatalf("CancelPointerCapture must not clear keyboard selection")
	}

	// Mouse drag is cleared without copying.
	ed.ClearSelection()
	textX := ed.gutterWidth() + 2
	_ = ed.HandleEvent(tea.MouseClickMsg{X: textX, Y: 0, Button: tea.MouseLeft})
	_ = ed.HandleEvent(tea.MouseMotionMsg{X: textX, Y: 3, Button: tea.MouseLeft})
	if !ed.IsMouseDragging() || !ed.HasSelection() {
		t.Fatal("expected active mouse drag selection")
	}
	ed.CancelPointerCapture()
	if ed.IsMouseDragging() {
		t.Fatal("CancelPointerCapture should clear mouseDragging")
	}
	if ed.HasSelection() {
		t.Fatal("CancelPointerCapture should drop in-progress mouse selection")
	}
}
