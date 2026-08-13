package activitybar

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
)

func TestHitIndexGlyphRows(t *testing.T) {
	if hitIndex(0) != -1 {
		t.Fatal("row 0 is padding")
	}
	if hitIndex(1) != 0 {
		t.Fatal("row 1 is Files")
	}
	if hitIndex(3) != 1 {
		t.Fatal("row 3 is Agents")
	}
	if hitIndex(5) != 2 {
		t.Fatal("row 5 is Terminals")
	}
	if hitIndex(7) != 3 {
		t.Fatal("row 7 is Git")
	}
	if hitIndex(2) != -1 {
		t.Fatal("gap rows are not hits")
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
