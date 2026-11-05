package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
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
			inStrip := x >= 0 && y >= 0 && y < StripHeight
			if res.Action == nil || res.Action.ID != "tabs.drag" || res.Action.Payload != (TabDrag{ID: "a", X: x, Y: y, InStrip: inStrip}) || !tabs.Dragging() {
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

// stripText returns the painted label row.
func stripText(host *Host, w int) string {
	buf := surface.NewCellBuffer(w, 2)
	host.Paint(buf)
	return rowText(buf, 0)
}

// Dragging inside the strip reorders it with sliding tabs; the host sees only
// a reorder proposal, never a placement drag, until the pointer leaves.
func TestTabsDragInsideStripReordersWithAnimation(t *testing.T) {
	now := time.Unix(100, 0)
	tabs := NewTabs("reorder/tabs", NewTokenSet(theme.DefaultDark()))
	tabs.now = func() time.Time { return now }
	tabs.SetItems([]TabItem{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}, {ID: "c", Label: "Gamma"}}, "a")
	tabs.SetDraggable(true)
	host := NewHost(theme.DefaultDark(), tabs)
	host.Resize(40, 2)
	host.Layout()
	if got := stripText(host, 40); !strings.HasPrefix(strings.TrimSpace(got), "Alpha  Beta  Gamma") {
		t.Fatalf("initial row %q", got)
	}
	host.Handle(tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	res := host.Handle(tea.MouseMotionMsg{X: 6, Y: 0, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != "tabs.drag" || !res.Action.Payload.(TabDrag).InStrip {
		t.Fatalf("drag inside the strip must report InStrip: %+v", res.Action)
	}
	// Alpha's center passed Beta's: Beta starts sliding left, not jumping.
	host.Paint(surface.NewCellBuffer(40, 2))
	now = now.Add(tabSlideDuration / 3)
	mid := tabs.paintRanges(40)
	beta := tabs.indexOfID("b")
	from, to := tabs.ranges[beta].labelStart, tabs.layoutRanges()[beta].labelStart
	if to >= from || mid[beta].labelStart <= to || mid[beta].labelStart >= from {
		t.Fatalf("Beta did not slide: shown at %d between %d and %d", mid[beta].labelStart, from, to)
	}
	if !tabs.NeedsAnimation() {
		t.Fatal("a sliding strip must request frames")
	}
	now = now.Add(tabSlideDuration)
	if got := strings.TrimSpace(stripText(host, 40)); !strings.HasPrefix(got, "Beta") {
		t.Fatalf("previewed order did not settle with Beta first: %q", got)
	}
	res = host.Handle(tea.MouseReleaseMsg{X: 6, Y: 0, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != "tabs.reorder" || res.Action.Payload != (TabReorder{ID: "a", BeforeID: "c"}) {
		t.Fatalf("release inside the strip must propose a reorder: %+v", res.Action)
	}
	if tabs.items[0].ID != "a" {
		t.Fatal("the strip must not reorder its own items; the host owns order")
	}
	// The host publishes the new order; nothing jumps back.
	tabs.SetItems([]TabItem{{ID: "b", Label: "Beta"}, {ID: "a", Label: "Alpha"}, {ID: "c", Label: "Gamma"}}, "a")
	host.Layout()
	now = now.Add(2 * tabSlideDuration)
	if got := strings.TrimSpace(stripText(host, 40)); !strings.HasPrefix(got, "Beta  Alpha  Gamma") {
		t.Fatalf("committed order %q", got)
	}
	if tabs.NeedsAnimation() {
		t.Fatal("a settled strip must not keep requesting frames")
	}
}

// Leaving the strip hands the gesture to pane placement and restores the
// strip order; a release outside is a placement drop, not a reorder.
func TestTabsDragLeavingStripStartsPlacement(t *testing.T) {
	tabs := NewTabs("place/tabs", NewTokenSet(theme.DefaultDark()))
	tabs.SetItems([]TabItem{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}}, "a")
	tabs.SetDraggable(true)
	host := NewHost(theme.DefaultDark(), tabs)
	host.Resize(30, 2)
	host.Layout()
	host.Handle(tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: 10, Y: 0, Button: tea.MouseLeft})
	res := host.Handle(tea.MouseMotionMsg{X: 10, Y: 6, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != "tabs.drag" || res.Action.Payload.(TabDrag).InStrip {
		t.Fatalf("leaving the strip must start placement: %+v", res.Action)
	}
	if layout := tabs.layoutRanges(); layout[0].labelStart != tabs.ranges[0].labelStart {
		t.Fatal("outside the strip the previewed reorder must be withdrawn")
	}
	res = host.Handle(tea.MouseReleaseMsg{X: 10, Y: 6, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != "tabs.drop" {
		t.Fatalf("release outside the strip must drop for placement: %+v", res.Action)
	}
}
