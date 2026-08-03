package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func TestTreePaintAndActivate(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	tree := NewTree("tree", tokens)
	tree.SetItems([]TreeItem{
		{ID: "/a", Label: "a", Depth: 0},
		{ID: "/a/b.go", Label: "b.go", Depth: 1},
	})
	tree.SelectIndex(1)
	tree.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 40, H: 10})
	buf := surface.NewCellBuffer(40, 10)
	tree.Paint(buf, 0, 0, PaintContext{Focused: true})
	if buf.Get(0, 0).Char == 0 && buf.Get(2, 0).Char == 0 {
		t.Fatal("expected painted cells")
	}

	res := tree.Handle(EventContext{LocalX: 5, LocalY: 1}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if !res.Handled || res.Action == nil || res.Action.ID != "tree.activate" {
		t.Fatalf("activate result=%+v", res)
	}
}

func TestTreeBreadcrumbOffsetsRows(t *testing.T) {
	tree := NewTree("tree", NewTokenSet(theme.DefaultDark()))
	tree.SetItems([]TreeItem{{ID: "/a", Label: "a"}})
	tree.SetBreadcrumb("parent/")
	tree.Layout(LayoutContext{}, Rect{W: 20, H: 5})
	if tree.rowAt(0) != -1 {
		t.Fatal("breadcrumb row should not hit an item")
	}
	if tree.rowAt(1) != 0 {
		t.Fatalf("row under breadcrumb should be index 0, got %d", tree.rowAt(1))
	}
}
