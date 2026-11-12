package ui

import (
	tea "charm.land/bubbletea/v2"
	"testing"
)

func TestNativeEditorActionsStayContextualAndHaveExactKeys(t *testing.T) {
	r := DefaultRegistry()
	for _, tc := range []struct {
		key tea.KeyPressMsg
		id  ActionID
	}{
		{tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, "edit.duplicateLine"},
		{tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt}, "select.nextOccurrence"},
		{tea.KeyPressMsg{Code: tea.KeyF3, Mod: tea.ModAlt}, "select.allOccurrences"},
		{tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl | tea.ModAlt | tea.ModShift}, "select.caretAbove"},
		{tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModCtrl | tea.ModAlt | tea.ModShift}, "select.caretBelow"},
		{tea.KeyPressMsg{Code: 'q', Mod: tea.ModAlt}, "select.shrink"},
		{tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt | tea.ModShift}, "select.subwordPrevious"},
		{tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt | tea.ModShift}, "select.subwordNext"},
		{tea.KeyPressMsg{Code: 'D', Mod: tea.ModCtrl | tea.ModShift}, "edit.duplicateLine"},
		{tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, "edit.lineBelow"},
		{tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl | tea.ModAlt}, "edit.lineBelow"},
		{tea.KeyPressMsg{Code: 'O', Mod: tea.ModCtrl | tea.ModAlt | tea.ModShift}, "edit.lineAbove"},
	} {
		native, ok := MatchEditorAction(tc.key)
		if !ok || native.ID != tc.id {
			t.Fatal(tc, native)
		}
		if got := r.MatchContext("editor", tc.key); got == nil || got.ID != tc.id {
			t.Fatal(tc, got)
		}
		if got := r.MatchGlobal(tc.key); got != nil {
			t.Fatal("native editing escaped to global", got)
		}
	}
	actions := EditorActions()
	actions[0].Bindings[0].Key = "escape"
	got, _ := FindEditorAction("edit.duplicateLine")
	if got.Bindings[0].Key != "ctrl+d" {
		t.Fatal("metadata aliases bindings")
	}
}
