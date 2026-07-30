package shellheader

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
)

// TestShellHeaderKeyboardContract documents that the header chrome is
// display-only: Handle is a no-op and keyboard chords route through viewmodel
// ActionRegistry / global dispatch, not the header Screen.
func TestShellHeaderKeyboardContract(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	host := ui.NewHost(th, screen)
	host.Resize(60, 2)
	host.Layout()

	keys := []tea.KeyPressMsg{
		{Code: tea.KeyTab},
		{Code: tea.KeyEnter},
		{Code: tea.KeyEsc},
		{Code: 'q', Text: "q", Mod: tea.ModCtrl},
	}
	for _, key := range keys {
		res := host.Handle(key)
		if res.Handled || res.Action != nil || res.Cmd != nil {
			t.Fatalf("header must not handle %v: %+v", key, res)
		}
	}
}
