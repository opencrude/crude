package shelltabs

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestEmptyRailDoubleClickDoesNotChangeExistingTabActions(t *testing.T) {
	s := New(nil)
	s.SetNewButton(true)
	s.SetProjection([]TabProjection{{ID: "1", Label: "File", Closable: true}}, "1", -1, false)
	h := ui.NewHost(theme.DefaultDark(), s)
	h.Resize(80, 2)
	h.Layout()
	click := func(x, y int) ui.EventResult {
		result := h.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		h.Handle(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
		return result
	}
	x := s.NewButtonBounds().X + s.NewButtonBounds().W + 2
	if result := click(x, 0); result.Action != nil {
		t.Fatalf("single empty click created a buffer: %+v", result.Action)
	}
	if result := click(x, 0); result.Action == nil || result.Action.ID != "tabs.new-buffer" {
		t.Fatalf("double click did not request a buffer: %+v", result.Action)
	}
	if result := click(x, 0); result.Action != nil {
		t.Fatalf("third click created another buffer: %+v", result.Action)
	}
	if result := click(s.NewButtonBounds().X, 0); result.Action == nil || result.Action.ID != "tabs.new" {
		t.Fatalf("new-session action changed: %+v", result.Action)
	}
	if result := click(x, 0); result.Action != nil {
		t.Fatalf("session click completed an old double click: %+v", result.Action)
	}
	if result := click(x, 1); result.Action != nil {
		t.Fatalf("baseline unexpectedly created a buffer: %+v", result.Action)
	}
}
