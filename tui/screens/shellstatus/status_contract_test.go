package shellstatus

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
)

// TestShellStatusKeyboardContract documents that the status Screen is
// display-only (Handle is a no-op). Actionable hint chords are registered in
// ui.ActionRegistry and dispatched from viewmodel keyboard routing — see
// viewmodel/status_keyboard_contract_test.go and status_help_test.go.
func TestShellStatusKeyboardContract(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	screen.SetWidth(80)
	host := ui.NewHost(th, screen)
	host.Resize(80, 1)
	host.Layout()

	keys := []tea.KeyPressMsg{
		{Code: tea.KeyTab},
		{Code: 's', Text: "s", Mod: tea.ModCtrl},
		{Code: tea.KeyEnter},
	}
	for _, key := range keys {
		res := host.Handle(key)
		if res.Handled || res.Action != nil || res.Cmd != nil {
			t.Fatalf("status bar must not handle %v: %+v", key, res)
		}
	}

	// Hints paint from registry segments but do not intercept keys at Screen level.
	reg := ui.DefaultRegistry()
	screen.SetContextHelp(reg, "", []ui.HintSegment{
		ui.ActionSeg("file.save"),
		ui.ActionSeg("edit.undo"),
	})
	if screen.View() == "" {
		t.Fatal("expected painted status view")
	}
}

// TestShellStatusHintSegmentsAreDisplayOnly verifies Hint segments render without
// creating a Handle path on the status Screen.
func TestShellStatusHintSegmentsAreDisplayOnly(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	screen.SetWidth(100)
	reg := ui.DefaultRegistry()
	screen.SetContextHelp(reg, "editor", []ui.HintSegment{
		ui.ActionSeg("file.save"),
	})
	view := screen.View()
	if view == "" {
		t.Fatal("expected non-empty status view with hint segments")
	}

	host := ui.NewHost(th, screen)
	host.Resize(100, 1)
	host.Layout()
	res := host.Handle(tea.KeyPressMsg{Code: 's', Text: "s", Mod: tea.ModCtrl})
	if res.Handled {
		t.Fatal("Ctrl+S must not be handled by status Screen; viewmodel dispatches it")
	}
}
