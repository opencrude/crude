package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

func TestActionBarRoutesOnlyFocusedAndHitButtons(t *testing.T) {
	bar := NewActionBar("actions", NewTokenSet(theme.DefaultDark()),
		ActionBarButton{ID: "primary", Label: "Primary", Action: "primary", Enabled: false},
		ActionBarButton{ID: "cancel", Label: "Cancel", Action: "cancel", Enabled: true},
	)
	host := NewHost(theme.DefaultDark(), bar)
	host.Resize(40, 1)
	host.Layout()
	host.Focus(bar.ID())
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Action != nil || result.Cmd != nil {
		t.Fatalf("disabled focused button activated another button: %#v", result)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action == nil || result.Action.ID != "cancel" {
		t.Fatalf("focused cancel result=%#v", result)
	}

	result = host.Handle(tea.MouseClickMsg{X: 32, Y: 0, Button: tea.MouseLeft})
	if result.Action == nil || result.Action.ID != "cancel" {
		t.Fatalf("cancel click result=%#v", result)
	}
}

func TestActionBarAlignStartPacksFromLeft(t *testing.T) {
	bar := NewActionBar("actions", NewTokenSet(theme.DefaultDark()),
		ActionBarButton{ID: "reset", Label: "Reset section", Action: "reset", Variant: VariantDanger, Enabled: true},
	)
	bar.SetAlignEnd(false)
	bar.Layout(LayoutContext{}, Rect{W: 40, H: 1})
	if bar.buttons[0].button.Bounds().X != 0 {
		t.Fatalf("start-aligned button x=%d want 0", bar.buttons[0].button.Bounds().X)
	}
}

func TestActionBarUpReturnsToForm(t *testing.T) {
	bar := NewActionBar("actions", NewTokenSet(theme.DefaultDark()),
		ActionBarButton{ID: "save", Label: "Save", Action: "save", Enabled: true},
		ActionBarButton{ID: "cancel", Label: "Cancel", Action: "cancel", Enabled: true},
	)
	res := bar.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
	if res.Action == nil || res.Action.ID != "actionbar.exit.backward" {
		t.Fatalf("Up on action bar result=%#v", res)
	}
	res = bar.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if res.Action == nil || res.Action.ID != "actionbar.exit.forward" {
		t.Fatalf("Down on action bar result=%#v", res)
	}
}

func TestDockedActionBarPacksFromLeftWithRule(t *testing.T) {
	bar := NewDockedActionBar("actions", NewTokenSet(theme.DefaultDark()),
		ActionBarButton{ID: "save", Label: "Save", Action: "save", Variant: VariantPrimary, Enabled: true},
	)
	if bar.PreferredHeight() != 2 {
		t.Fatalf("docked action bar height=%d, want 2", bar.PreferredHeight())
	}
	bar.Layout(LayoutContext{}, Rect{W: 40, H: 2})
	if bar.buttons[0].button.Bounds().X != 0 || bar.buttons[0].button.Bounds().Y != 1 {
		t.Fatalf("docked button bounds=%v, want x=0 y=1", bar.buttons[0].button.Bounds())
	}
}
