package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestVirtualListSelectsAndActivatesPayload(t *testing.T) {
	list := NewVirtualList("results", nil)
	list.SetActivateAction("find.open")
	list.SetItems([]VirtItem{
		{ID: "a", Label: "a.go", Payload: 0},
		{ID: "b", Label: "b.go", Payload: 1},
	})
	list.Layout(LayoutContext{}, Rect{W: 30, H: 1})

	list.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if list.SelectedIndex() != 1 {
		t.Fatalf("selected=%d", list.SelectedIndex())
	}
	result := list.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
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

func TestVirtualListOpaqueRowFillCanBeDisabled(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	list := NewVirtualList("chat", tokens)
	list.SetOpaqueRowFill(false)
	list.SetRowPainter(func(buf *surface.CellBuffer, x, y, width int, item VirtItem, localRow int, state ControlState, ctx PaintContext, ts *TokenSet) {
		buf.Set(x+1, y, cell.NewCell('x', ts.Color(ColorFg), cell.NoColor, 0))
	})
	list.SetItems([]VirtItem{{ID: "m1", Height: 1, Label: "hello"}})
	list.Layout(LayoutContext{}, Rect{W: 8, H: 1})
	buf := surface.NewCellBuffer(8, 1)
	list.Paint(buf, 0, 0, PaintContext{Focused: true})
	popup := tokens.Color(ColorPopupBg)
	if buf.Get(0, 0).BG.Equal(popup) {
		t.Fatal("disabled row fill must not paint ListRowColors into untouched columns")
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

func TestVirtualListRebuildPrefixReusesCapacity(t *testing.T) {
	list := NewVirtualList("reuse", nil)
	list.SetSelectionAnchorsScroll(false)
	items := []VirtItem{{ID: "a", Height: 2, Label: "alpha"}, {ID: "b", Height: 1, Label: "bravo"}, {ID: "c", Height: 3, Label: "charlie"}}
	list.SetItems(items)
	list.Layout(LayoutContext{}, Rect{W: 20, H: 2})
	list.SelectIndex(1)
	list.Scroll().ScrollTo(2)
	start := &list.prefix[0]
	revision := list.StructureRevision()
	if allocations := testing.AllocsPerRun(100, func() { list.SetItems(items) }); allocations != 0 {
		t.Fatalf("warm index rebuild allocated %.0f times; want zero", allocations)
	}
	if &list.prefix[0] != start || list.StructureRevision() != revision+101 {
		t.Fatal("index capacity was not reused or structural revision did not advance")
	}
	list.UpdateItem(0, VirtItem{ID: "a", Height: 3, Label: "alpha"})
	if &list.prefix[0] != start || list.Scroll().Content() != 7 || list.ItemIndexAtRow(3) != 1 ||
		list.SelectedIndex() != 1 || list.Scroll().Offset() != 2 {
		t.Fatal("height delta broke index, selection or disarmed scroll")
	}
	list.SetItems([]VirtItem{{ID: "latest", Height: 1, Label: "latest"}})
	if &list.prefix[0] != start || len(list.prefix) != 2 || list.prefix[1] != 1 ||
		list.Scroll().Offset() != 0 || list.ItemIndexAtRow(1) != -1 {
		t.Fatal("shrinking retained stale prefix rows or failed to clamp scroll")
	}
	buf := surface.NewCellBuffer(20, 2)
	list.Paint(buf, 0, 0, PaintContext{})
	if buf.Get(0, 0).Char != 'l' {
		t.Fatal("latest replacement was not painted")
	}
	list.SetItems(nil)
	if &list.prefix[0] != start || len(list.prefix) != 1 || list.prefix[0] != 0 || list.SelectedIndex() != -1 {
		t.Fatal("empty replacement retained old index or selection")
	}
	list.SetItems(items)
	if &list.prefix[0] != start || list.ItemIndexAtRow(3) != 1 {
		t.Fatal("regrowth did not rebuild the reused prefix")
	}
}

func TestVirtualListNoninteractiveRowsNeverSelectOrActivate(t *testing.T) {
	list := NewVirtualList("resources", nil)
	list.SetItems([]VirtItem{
		{ID: "heading", Disabled: true},
		{ID: "one", Height: 2},
		{ID: "status", Disabled: true},
		{ID: "two", Height: 2},
		{ID: "loading", Disabled: true},
	})
	list.Layout(LayoutContext{}, Rect{W: 20, H: 7})
	if got, _ := list.Selected(); got.ID != "one" {
		t.Fatal("initial cursor selected the heading")
	}
	for _, step := range []struct {
		key  rune
		want string
	}{
		{tea.KeyDown, "two"}, {tea.KeyDown, "two"}, {tea.KeyUp, "one"},
		{tea.KeyHome, "one"}, {tea.KeyPgDown, "two"}, {tea.KeyPgUp, "one"}, {tea.KeyEnd, "two"},
	} {
		list.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: step.key}))
		if got, _ := list.Selected(); got.ID != step.want {
			t.Fatalf("key %d selected %s, want %s", step.key, got.ID, step.want)
		}
	}
	list.Scroll().ScrollTo(0)
	res := list.Handle(EventContext{LocalY: 3}, Event{Kind: EventMouseClick, MouseClick: tea.MouseClickMsg{Button: tea.MouseLeft}})
	if res.Action != nil || list.SelectIndex(2) {
		t.Fatal("disabled status row activated or selected")
	}
	list.SetItems([]VirtItem{{ID: "none", Disabled: true}})
	if list.SelectedIndex() != -1 {
		t.Fatal("placeholder-only list has a cursor")
	}
	if res := list.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter})); res.Action != nil {
		t.Fatal("placeholder-only list activated")
	}
}
