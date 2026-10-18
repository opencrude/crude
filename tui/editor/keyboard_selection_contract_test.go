package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestKeyboardSelectionKeepsInsertionBoundaryDespiteBlockCaret(t *testing.T) {
	ed := newPointerTestEditor(t, "abcdef", 40, 8)
	ed.SetCursorPosition(0, 3) // insertion point before d, not a selection of d
	for _, step := range []struct {
		code rune
		want string
	}{{tea.KeyLeft, "c"}, {tea.KeyLeft, "bc"}, {tea.KeyRight, "c"}, {tea.KeyRight, ""}, {tea.KeyRight, "d"}} {
		ed.HandleEvent(tea.KeyPressMsg{Code: step.code, Mod: tea.ModShift})
		if got := ed.SelectedText(); got != step.want {
			t.Fatalf("key %v selected %q want %q", step.code, got, step.want)
		}
		if ed.IsMouseDragging() {
			t.Fatal("keyboard movement created pointer owner")
		}
	}
}
