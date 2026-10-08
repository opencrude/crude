package ui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/theme"
)

func TestTabsNativeHostDragGesture(t *testing.T) {
	for _, delta := range [][2]int{{2, 0}, {-2, 0}, {0, 1}, {0, -1}} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			tabs := NewTabs("drag/tabs", NewTokenSet(theme.DefaultDark()))
			tabs.SetItems([]TabItem{{ID: "a", Label: "Alpha", Closable: true}, {ID: "b", Label: "Beta"}}, "b")
			tabs.SetDraggable(true)
			host := NewHost(theme.DefaultDark(), tabs)
			host.Resize(30, 2)
			host.Layout()
			res := host.Handle(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
			if res.Action == nil || res.Action.ID != "tabs.activate" || tabs.ActiveID() != "a" || !host.HasPointerCapture() || tabs.Dragging() {
				t.Fatalf("ordinary press must activate and capture: %+v", res)
			}
			res = host.Handle(tea.MouseMotionMsg{X: 2, Y: 0, Button: tea.MouseLeft})
			if res.Action != nil || tabs.Dragging() {
				t.Fatal("one column must not start drag")
			}
			x, y := 1+delta[0], delta[1]
			res = host.Handle(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft})
			if res.Action == nil || res.Action.ID != "tabs.drag" || res.Action.Payload != (TabDrag{ID: "a", X: x, Y: y}) || !tabs.Dragging() {
				t.Fatalf("threshold action: %+v", res)
			}
			host.Handle(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseRight})
			if !host.HasPointerCapture() || !tabs.Dragging() {
				t.Fatal("wrong-button release must preserve gesture")
			}
			res = host.Handle(tea.MouseReleaseMsg{X: 80, Y: 10, Button: tea.MouseLeft})
			if res.Action == nil || res.Action.ID != "tabs.drop" || res.Action.Payload != (TabDrag{ID: "a", X: 80, Y: 10}) || tabs.Dragging() || host.HasPointerCapture() {
				t.Fatalf("outside drop: %+v", res)
			}
			if tabs.ActiveID() != "a" || tabs.items[0].ID != "a" {
				t.Fatal("drag must not mutate order or selection")
			}
		})
	}
}

func TestTabsNativeHostCancelAndClickOnly(t *testing.T) {
	tabs := NewTabs("tabs", NewTokenSet(theme.DefaultDark()))
	tabs.SetItems([]TabItem{{ID: "a", Label: "Alpha", Closable: true}}, "a")
	host := NewHost(theme.DefaultDark(), tabs)
	host.Resize(30, 2)
	host.Layout()
	host.Handle(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if res := host.Handle(tea.MouseMotionMsg{X: 10, Y: 4, Button: tea.MouseLeft}); res.Action != nil || tabs.Dragging() {
		t.Fatal("default strips must remain click-only")
	}
	host.CancelPointerCapture()
	tabs.SetDraggable(true)
	host.Handle(tea.MouseMotionMsg{X: 1, Y: 0})
	if tabs.HoverIndex() != 0 || tabs.Dragging() {
		t.Fatal("hover must not drag")
	}
	host.Handle(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if res := host.Handle(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft}); res.Action != nil || host.HasPointerCapture() {
		t.Fatal("ordinary click must not drop")
	}
	host.Handle(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: 3, Y: 0, Button: tea.MouseLeft})
	host.CancelPointerCapture()
	if tabs.Dragging() || tabs.ActiveID() != "a" {
		t.Fatal("cancel must clear gesture, not selection")
	}
	if res := host.Handle(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft}); res.Action != nil {
		t.Fatal("cancelled drag cannot drop")
	}
	x := tabs.CloseLocalX(0)
	res := host.Handle(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != "tabs.close" {
		t.Fatal("close press must still close")
	}
	if res := host.Handle(tea.MouseMotionMsg{X: x + 3, Y: 1, Button: tea.MouseLeft}); res.Action != nil || tabs.Dragging() {
		t.Fatal("close affordance must not arm drag")
	}
}

func TestTabsDragOverflowAndRemovedTab(t *testing.T) {
	tabs := NewTabs("tabs", NewTokenSet(theme.DefaultDark()))
	items := make([]TabItem, 30)
	for i := range items {
		items[i] = TabItem{ID: fmt.Sprint(i), Label: "Long tab label", Closable: true}
	}
	tabs.SetItems(items, "0")
	tabs.SetDraggable(true)
	host := NewHost(theme.DefaultDark(), tabs)
	host.Resize(30, 2)
	host.Layout()
	r := tabs.scrollButtons[1]
	host.Handle(tea.MouseClickMsg{X: r.X, Y: 0, Button: tea.MouseLeft})
	if res := host.Handle(tea.MouseMotionMsg{X: 45, Y: 5, Button: tea.MouseLeft}); res.Action != nil || tabs.Dragging() {
		t.Fatal("overflow arrow cannot drag")
	}
	host.CancelPointerCapture()
	host.Handle(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	tabs.SetItems(nil, "")
	if tabs.Dragging() {
		t.Fatal("removed source must clear gesture")
	}
	if res := host.Handle(tea.MouseReleaseMsg{X: 45, Y: 5, Button: tea.MouseLeft}); res.Action != nil {
		t.Fatal("removed source cannot drop")
	}
}
