package statusoverview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
)

// A docked panel copies the selected row with Ctrl+C, so its text is copyable
// without a separate reader window.
func TestDockedCtrlCCopiesSelectedItemText(t *testing.T) {
	projection := testProjection()
	screen := NewDocked(theme.DefaultDark(), projection)
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(100, 24)
	host.Layout()
	host.Focus(ListID)
	if !screen.Select("agent:one") {
		t.Fatal("item missing")
	}
	result := host.Handle(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if result.Cmd == nil {
		t.Fatal("Ctrl+C did not copy the selected item")
	}
	text := screen.selectedItemText()
	if text == "" || !strings.Contains(text, "One") {
		t.Fatalf("selected item text = %q", text)
	}
}
