package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestToggleButtonEmitsActionAndReportsSelection(t *testing.T) {
	button := NewToggleButton("case", "Cc", "find.toggle-case", nil)
	button.SetChecked(true)
	button.SetCancelAction("find.close")
	result := button.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeySpace}))
	if result.Action == nil || result.Action.ID != "find.toggle-case" {
		t.Fatalf("action=%#v", result.Action)
	}
	if !button.Semantics().Selected {
		t.Fatal("checked toggle should report selected semantics")
	}
	cancel := button.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if cancel.Action == nil || cancel.Action.ID != "find.close" {
		t.Fatalf("cancel=%#v", cancel.Action)
	}
}
