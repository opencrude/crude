package activitybar

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestHitIndexGlyphRows(t *testing.T) {
	if hitIndex(0) != 0 || hitIndex(1) != 0 {
		t.Fatal("rows 0-1 are the Files target")
	}
	if hitIndex(2) != 1 || hitIndex(3) != 1 {
		t.Fatal("rows 2-3 are Agents")
	}
	if hitIndex(4) != 2 || hitIndex(5) != 2 {
		t.Fatal("rows 4-5 are Terminals")
	}
	if hitIndex(6) != 3 || hitIndex(7) != 3 {
		t.Fatal("rows 6-7 are Source Control")
	}
	if hitIndex(8) != -1 {
		t.Fatal("rows below the activity targets are not hits")
	}
}

func TestClickEmitsSelect(t *testing.T) {
	s := New(theme.DefaultDark())
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: Width, H: 20})
	res := s.Handle(ui.EventContext{LocalY: 5}, ui.Event{
		Kind:       ui.EventMouseClick,
		MouseClick: tea.MouseClickMsg{Button: tea.MouseLeft},
	})
	if !res.Handled || res.Action == nil || res.Action.ID != ActionSelect {
		t.Fatalf("result=%+v", res)
	}
	view, ok := res.Action.Payload.(View)
	if !ok || view != ViewTerminals {
		t.Fatalf("payload=%v", res.Action.Payload)
	}
}

func TestHoverPaintsOnlyWhileHostHoversRail(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: Width, H: 20})
	res := s.Handle(ui.EventContext{LocalY: 3}, ui.Event{Kind: ui.EventMouseMotion})
	if !res.Handled {
		t.Fatal("motion over a glyph row must be handled")
	}
	hover := ui.NewTokenSet(th).Color(ui.ColorHover)

	buf := surface.NewCellBuffer(Width, 20)
	s.Paint(buf, 0, 0, ui.PaintContext{Hovered: true})
	if !buf.Get(1, 3).BG.Equal(hover) {
		t.Fatal("hovered rail row should paint hover bg while host hovers the rail")
	}

	buf = surface.NewCellBuffer(Width, 20)
	s.Paint(buf, 0, 0, ui.PaintContext{Hovered: false})
	if buf.Get(1, 3).BG.Equal(hover) {
		t.Fatal("stale hover must not paint after the pointer left the rail")
	}
}
