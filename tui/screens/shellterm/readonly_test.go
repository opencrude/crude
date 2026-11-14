package shellterm

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
)

func TestReadOnlyTerminalNeverSendsInputOrTakeControl(t *testing.T) {
	s := New(theme.DefaultDark(), "command", "Output")
	s.SetReadOnly(true)
	s.SetControlled(true)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 80, H: 12})
	s.TakePendingCommands()
	for _, key := range []tea.KeyPressMsg{{Code: 'x', Text: "x"}, {Code: 'c', Mod: tea.ModCtrl}, {Code: tea.KeyEnter}} {
		result := s.handleKey(key)
		if result.Action != nil || result.Cmd != nil {
			t.Fatalf("reader sent input or requested control: %+v", result)
		}
	}
	s.PasteText("do not send")
	s.SetTermFocused(true)
	if s.PasteActive() || len(s.TakePendingCommands()) != 0 {
		t.Fatal("a passive viewer queued input")
	}
	s.SetExited(true)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 90, H: 15})
	found := false
	for _, c := range s.TakePendingCommands() {
		if c.Kind == CommandResize && c.Cols == 90 && c.Rows == 15 {
			found = true
		}
	}
	if !found {
		t.Fatal("a completed viewer cannot resize")
	}
}
