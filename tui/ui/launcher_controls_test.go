package ui

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
	"testing"
)

func TestActionLinkSharesButtonActivation(t *testing.T) {
	link := NewActionLink("link", "New Session", "new")
	for _, event := range []Event{NormalizeEvent(tea.KeyPressMsg{Code: tea.KeyEnter}), NormalizeEvent(tea.KeyPressMsg{Code: tea.KeySpace}), NormalizeEvent(tea.MouseClickMsg{Button: tea.MouseLeft})} {
		if result := link.Handle(EventContext{}, event); result.Action == nil || result.Action.ID != "new" {
			t.Fatal("action link lost button semantics")
		}
		link.SetDisabled(true)
		if link.Handle(EventContext{}, event).Action != nil {
			t.Fatal("disabled link activated")
		}
		link.SetDisabled(false)
	}
}
func TestProgressRuleMovesInkNotGeometryAndStops(t *testing.T) {
	rule := NewProgressRule("rule", NewTokenSet(theme.DefaultDark()))
	rule.Layout(LayoutContext{}, Rect{X: 2, Y: 1, W: 80, H: 1})
	rule.SetActive(true)
	first, second := surface.NewCellBuffer(84, 3), surface.NewCellBuffer(84, 3)
	rule.SetFrame(0)
	rule.Paint(first, 0, 0, PaintContext{})
	rule.SetFrame(1400)
	rule.Paint(second, 0, 0, PaintContext{})
	changed := false
	for x := 2; x < 82; x++ {
		a, b := first.Get(x, 1), second.Get(x, 1)
		if a.Char != b.Char || a.Width != b.Width || a.BG != b.BG {
			t.Fatal("loading motion changed geometry/background")
		}
		changed = changed || a.FG != b.FG
	}
	if !changed {
		t.Fatal("rule did not animate")
	}
	rule.SetActive(false)
	rule.Paint(first, 0, 0, PaintContext{})
	rule.SetFrame(2400)
	rule.Paint(second, 0, 0, PaintContext{})
	for x := 2; x < 82; x++ {
		if first.Get(x, 1) != second.Get(x, 1) {
			t.Fatal("settled rule kept animating")
		}
	}
}
