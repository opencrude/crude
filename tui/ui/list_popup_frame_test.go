package ui

import (
	"testing"

	"crude/tui/theme"
)

func TestBuildListPopupFrame_CompletionShape(t *testing.T) {
	th := theme.DefaultDark()
	items := []ListItem{
		{ID: "0", Icon: "ƒ", Label: "Foo", Detail: "func()", Payload: 0},
		{ID: "1", Icon: "v", Label: "bar", Detail: "int", Payload: 1},
	}
	cw := BuildListPopupFrame(th, ListPopupSpec{
		ID:       "completion-popup",
		Title:    "Completions · 2",
		Items:    items,
		Selected: 1,
		Width:    40,
		Height:   8,
		Closable: true,
	})
	if cw == nil {
		t.Fatal("nil ComponentWidget")
	}
	list := ListPopupList(cw)
	if list == nil {
		t.Fatal("expected list inside frame")
	}
	if list.SelectedIndex() != 1 {
		t.Fatalf("selected=%d want 1", list.SelectedIndex())
	}
	if got := len(list.Items()); got != 2 {
		t.Fatalf("items=%d want 2", got)
	}
	if list.Items()[0].Icon != "ƒ" || list.Items()[0].Label != "Foo" {
		t.Fatalf("item0=%+v", list.Items()[0])
	}
}

func TestBuildListPopupFrame_OutlineIndent(t *testing.T) {
	th := theme.DefaultDark()
	items := []ListItem{
		{ID: "0", Icon: "C", Label: "Pkg", Payload: 0},
		{ID: "1", Icon: "  ƒ", Label: "Method", Payload: 1}, // depth indent in Icon
	}
	cw := BuildListPopupFrame(th, ListPopupSpec{
		ID:       "outline-popup",
		Title:    "Outline · 2",
		Items:    items,
		Selected: 0,
		Width:    36,
		Height:   6,
		Closable: true,
	})
	list := ListPopupList(cw)
	if list == nil {
		t.Fatal("expected list")
	}
	if list.Items()[1].Icon != "  ƒ" {
		t.Fatalf("indent icon=%q", list.Items()[1].Icon)
	}
}
