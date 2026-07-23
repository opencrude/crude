package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestVirtualListSelectsAndActivatesPayload(t *testing.T) {
	list := NewVirtualList("results", nil)
	list.SetActivateAction("find.open")
	list.SetItems([]VirtItem{
		{ID: "a", Label: "a.go", Payload: 0},
		{ID: "b", Label: "b.go", Payload: 1},
	})
	list.Layout(LayoutContext{}, Rect{W: 30, H: 1})

	list.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyDown})
	if list.SelectedIndex() != 1 {
		t.Fatalf("selected=%d", list.SelectedIndex())
	}
	result := list.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action == nil || result.Action.ID != "find.open" {
		t.Fatalf("action=%#v", result.Action)
	}
	item, ok := result.Action.Payload.(VirtItem)
	if !ok || item.Payload != 1 {
		t.Fatalf("payload=%#v", result.Action.Payload)
	}
}
