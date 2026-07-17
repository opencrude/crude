package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

// End-to-end: search field + list in a vertical stack under Host.
func TestHost_SearchAndListComposition(t *testing.T) {
	th := theme.DefaultDark()
	ts := NewTokenSet(th)

	field := NewTextField("search", ts)
	field.SetPrompt(">")
	field.SetPlaceholder("filter")
	field.SetSubmitAction("search.submit")

	list := NewList("results", ts)
	list.SetActivateAction("results.open")
	all := []ListItem{
		{ID: "1", Label: "apple", Detail: "fruit"},
		{ID: "2", Label: "apricot", Detail: "fruit"},
		{ID: "3", Label: "banana", Detail: "fruit"},
		{ID: "4", Label: "carrot", Detail: "veg"},
	}
	list.SetItems(all)

	root := NewStack("root", Vertical, 0, field, list)
	h := NewHost(th, root)
	h.Register(field)
	h.Register(list)
	h.Resize(40, 12)
	h.Focus("search")

	// Type "ap"
	h.Handle(tea.KeyPressMsg{Code: 'a', Text: "a"})
	h.Handle(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if field.Value() != "ap" {
		t.Fatal(field.Value())
	}

	// Screen filters list based on field (screen responsibility)
	q := field.Value()
	var filtered []ListItem
	for _, it := range all {
		if len(q) == 0 || containsFold(it.Label, q) {
			filtered = append(filtered, it)
		}
	}
	list.SetItems(filtered)
	h.MarkLayout()

	if list.SelectedIndex() != 0 || len(list.Items()) != 2 {
		t.Fatalf("filtered=%d sel=%d", len(list.Items()), list.SelectedIndex())
	}

	// Move focus to list and select second
	h.Focus("results")
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	res := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	item, ok := res.Action.Payload.(ListItem)
	if !ok || item.Label != "apricot" {
		t.Fatalf("%+v", res.Action)
	}

	// Paint full tree
	buf := surface.NewCellBuffer(40, 12)
	h.Paint(buf)
	// Theme switch preserves state
	h.SetTheme(th)
	if field.Value() != "ap" {
		t.Fatal("theme switch lost value")
	}
	if h.Tokens().Version < 2 {
		t.Fatal("token version")
	}
}

func containsFold(s, sub string) bool {
	// simple substring for test
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestHost_TextFieldClickFocus(t *testing.T) {
	th := theme.DefaultDark()
	ts := NewTokenSet(th)
	field := NewTextField("search", ts)
	btn := NewButton("go", "Go", "go")
	btn.SetTokens(ts)
	root := NewStack("root", Horizontal, 1, field, btn)
	h := NewHost(th, root)
	h.Register(field)
	h.Register(btn)
	h.Resize(40, 3)
	// Click into field region (x=0)
	h.Handle(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if h.Focused() != "search" {
		t.Fatalf("focus=%q", h.Focused())
	}
}
