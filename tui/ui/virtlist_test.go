package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
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

func TestVirtualListPaintsMultiRowLocalRows(t *testing.T) {
	list := NewVirtualList("chat", nil)
	var seen []int
	list.SetRowPainter(func(buf *surface.CellBuffer, x, y, width int, item VirtItem, localRow int, state ControlState, ctx PaintContext, tokens *TokenSet) {
		seen = append(seen, localRow)
		ch := rune('0' + localRow)
		buf.Set(x, y, cell.NewCell(ch, cell.NoColor, cell.NoColor, 0))
	})
	list.SetItems([]VirtItem{
		{ID: "m1", Height: 3, Label: "hello"},
	})
	list.Layout(LayoutContext{}, Rect{W: 10, H: 3})

	buf := surface.NewCellBuffer(10, 3)
	list.Paint(buf, 0, 0, PaintContext{})

	if len(seen) != 3 {
		t.Fatalf("painter calls=%d want 3; seen=%v", len(seen), seen)
	}
	for i, lr := range seen {
		if lr != i {
			t.Fatalf("localRow[%d]=%d", i, lr)
		}
	}
	for y := 0; y < 3; y++ {
		c := buf.Get(0, y)
		want := rune('0' + y)
		if c.Char != want {
			t.Fatalf("row %d char=%q want %q", y, string(c.Char), string(want))
		}
	}
}

func TestScrollModelAutoScrollStickToBottom(t *testing.T) {
	s := NewScrollModel()
	s.SetViewport(5)
	s.SetContent(20)
	if s.AutoScroll() {
		t.Fatal("auto-scroll should default off")
	}
	s.SetAutoScroll(true)
	s.ApplyAutoScroll()
	if s.Offset() != s.MaxOffset() {
		t.Fatalf("offset=%d max=%d", s.Offset(), s.MaxOffset())
	}
	s.ScrollBy(-10)
	if s.ShouldAutoScroll() {
		t.Fatal("scrolled up should disarm auto-scroll")
	}
	if s.AutoScroll() {
		t.Fatal("AutoScroll flag should be false")
	}
	s.ScrollTo(s.MaxOffset())
	if !s.ShouldAutoScroll() {
		t.Fatal("at bottom should re-arm")
	}
}

func TestVirtualListSetItemsKeepsStickToBottom(t *testing.T) {
	list := NewVirtualList("chat", nil)
	list.Scroll().SetAutoScroll(true)
	list.Layout(LayoutContext{}, Rect{W: 20, H: 4})
	list.SetItems([]VirtItem{
		{ID: "a", Height: 2, Label: "a"},
		{ID: "b", Height: 2, Label: "b"},
		{ID: "c", Height: 2, Label: "c"},
	})
	if list.Scroll().Offset() != list.Scroll().MaxOffset() {
		t.Fatalf("want bottom offset=%d got=%d", list.Scroll().MaxOffset(), list.Scroll().Offset())
	}
	list.SetBottomPadding(2)
	if list.Scroll().Content() != 8 {
		t.Fatalf("content=%d want 8 (6 item + 2 pad)", list.Scroll().Content())
	}
	list.SetItems([]VirtItem{
		{ID: "a", Height: 2, Label: "a"},
		{ID: "b", Height: 2, Label: "b"},
		{ID: "c", Height: 2, Label: "c"},
		{ID: "d", Height: 4, Label: "d"},
	})
	if list.Scroll().Offset() != list.Scroll().MaxOffset() {
		t.Fatalf("after grow offset=%d max=%d", list.Scroll().Offset(), list.Scroll().MaxOffset())
	}
}
