package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSearchFieldDebouncesOnlyItsCurrentValue(t *testing.T) {
	field := NewSearchField("search", nil)
	field.SetDebounce(0)
	field.Layout(LayoutContext{}, Rect{W: 20, H: 1})

	first := field.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: 'a', Text: "a"})
	if first.Cmd == nil {
		t.Fatal("typing should schedule a debounce")
	}
	firstMsg, ok := first.Cmd().(SearchDebouncedMsg)
	if !ok {
		t.Fatalf("first debounce=%T", first.Cmd())
	}
	if !field.AcceptsDebounce(firstMsg) {
		t.Fatal("current debounce should be accepted")
	}

	field.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: 'b', Text: "b"})
	if field.AcceptsDebounce(firstMsg) {
		t.Fatal("a newer value should invalidate the prior debounce")
	}
}
