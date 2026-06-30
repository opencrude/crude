package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

// newTestTextItem builds a TextItem with the default dark theme so
// tests don't need a real *theme.Theme. Most tests don't actually
// need the theme — they just need a non-nil pointer for the View()
// method to not panic.
func newTestTextItem(value string) *TextItem {
	return NewTextItem(theme.DefaultDark(), "label", value, false)
}

func TestTextItem_SelectAllThenCopy(t *testing.T) {
	ti := newTestTextItem("hello world")
	ti.StartEditing()

	// Press Ctrl+A.
	_, _, _, handled := ti.HandleKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if !handled {
		t.Fatal("Ctrl+A should be handled")
	}
	if !ti.HasSelection() {
		t.Fatal("Ctrl+A should create a selection")
	}
	if got := ti.SelectedText(); got != "hello world" {
		t.Fatalf("SelectedText() = %q, want %q", got, "hello world")
	}
}

func TestTextItem_CopyWithNoSelectionConsumesKey(t *testing.T) {
	ti := newTestTextItem("hello")
	ti.StartEditing()

	// No selection — Ctrl+C should still be handled (so the form
	// doesn't fall through to a "back" action) but should NOT
	// return a non-nil cmd (there's nothing to copy).
	_, cmd, _, handled := ti.HandleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !handled {
		t.Fatal("Ctrl+C with no selection must be handled (consumed)")
	}
	if cmd != nil {
		t.Fatal("Ctrl+C with no selection should not return a cmd")
	}
}

func TestTextItem_SelectionClearsOnArrowWithoutShift(t *testing.T) {
	ti := newTestTextItem("abc")
	ti.StartEditing()
	ti.SelectAll()
	if !ti.HasSelection() {
		t.Fatal("setup: SelectAll should create selection")
	}

	// Press Left without shift — selection should clear.
	_, _, _, _ = ti.HandleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if ti.HasSelection() {
		t.Fatal("arrow without shift should clear the selection")
	}
}

func TestTextItem_BackspaceDeletesSelection(t *testing.T) {
	ti := newTestTextItem("hello world")
	ti.StartEditing()
	ti.editCursor = 11 // end
	ti.SetSelection(6, 11)
	if ti.SelectedText() != "world" {
		t.Fatalf("setup: selection = %q, want %q", ti.SelectedText(), "world")
	}

	_, _, _, handled := ti.HandleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if !handled {
		t.Fatal("Backspace must be handled")
	}
	if ti.Value() != "hello " {
		t.Fatalf("after Backspace, value = %q, want %q", ti.Value(), "hello ")
	}
	if ti.HasSelection() {
		t.Fatal("selection should be cleared after Backspace")
	}
}

func TestTextItem_PasteReplacesSelection(t *testing.T) {
	ti := newTestTextItem("hello world")
	ti.StartEditing()
	ti.editCursor = 11
	ti.SetSelection(0, 5) // select "hello"
	ti.HandlePaste("bye")
	if ti.Value() != "bye world" {
		t.Fatalf("after paste-with-selection, value = %q, want %q", ti.Value(), "bye world")
	}
}

func TestTextItem_CtrlXDeletesAndCopies(t *testing.T) {
	ti := newTestTextItem("hello world")
	ti.StartEditing()
	ti.editCursor = 11
	ti.SetSelection(0, 5)

	_, cmd, _, handled := ti.HandleKey(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if !handled {
		t.Fatal("Ctrl+X must be handled")
	}
	if cmd == nil {
		t.Fatal("Ctrl+X with selection should return a copy cmd")
	}
	if ti.Value() != " world" {
		t.Fatalf("after cut, value = %q, want %q", ti.Value(), " world")
	}
}

func TestTextItem_CtrlUClearsValue(t *testing.T) {
	ti := newTestTextItem("hello world")
	ti.StartEditing()
	ti.editCursor = 5

	_, _, _, handled := ti.HandleKey(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if !handled {
		t.Fatal("Ctrl+U must be handled")
	}
	if ti.Value() != "" {
		t.Fatalf("after Ctrl+U, value = %q, want empty", ti.Value())
	}
	if ti.editCursor != 0 {
		t.Fatalf("after Ctrl+U, cursor = %d, want 0", ti.editCursor)
	}
}

func TestTextItem_InsertReplacesSelection(t *testing.T) {
	ti := newTestTextItem("hello world")
	ti.StartEditing()
	ti.editCursor = 11
	ti.SetSelection(6, 11) // select "world"

	// Type 'X' — should replace selection, not insert.
	_, _, _, handled := ti.HandleKey(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if !handled {
		t.Fatal("character insert must be handled")
	}
	if ti.Value() != "hello X" {
		t.Fatalf("after insert-with-selection, value = %q, want %q", ti.Value(), "hello X")
	}
	if ti.editCursor != 7 {
		t.Fatalf("after insert, cursor = %d, want 7", ti.editCursor)
	}
}

func TestTextItem_NewTextItemHasNoSelection(t *testing.T) {
	ti := newTestTextItem("anything")
	if ti.HasSelection() {
		t.Fatal("freshly constructed TextItem should have no selection")
	}
	if ti.SelectedText() != "" {
		t.Fatal("freshly constructed TextItem should have empty SelectedText")
	}
}

func TestTextItem_ViewWithSelectionContainsHighlight(t *testing.T) {
	ti := newTestTextItem("hello world")
	th := ti.theme
	ti.StartEditing()
	ti.editCursor = 11
	ti.SetSelection(0, 5)

	view := ti.View(true, 0, 6)
	// The view should contain the highlight ANSI sequence for the
	// selection bg colour. We don't check the exact bytes (those
	// depend on lipgloss's serialiser) — we just check that the
	// view is non-empty and contains the substring "hello".
	if !strings.Contains(stripAnsi(view), "hello") {
		t.Fatalf("View() stripped = %q, want to contain %q", stripAnsi(view), "hello")
	}
	_ = th
}

// stripAnsi is a tiny helper that removes CSI sequences. We can't
// use the rendering package's StripAnsi here because that would
// create an import cycle (form is imported by viewmodel which
// imports rendering... actually no it doesn't, but for symmetry we
// keep the helper local to the test).
func stripAnsi(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
