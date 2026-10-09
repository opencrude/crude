package shellterm

import (
	"crude/tui/ui"
	"crude/workspace"
	"testing"
)

func TestScreenTinyLayoutRequestsSizeWithoutPreclaim(t *testing.T) {
	s, _ := newTestScreen(t)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 2, H: 1})
	commands := s.TakePendingCommands()
	if len(commands) != 1 || commands[0].Kind != CommandResize || commands[0].Cols != workspace.MinTerminalCols || commands[0].Rows != workspace.MinTerminalRows {
		t.Fatalf("intents=%+v", commands)
	}
	if s.cols != 40 || s.rows != 11 {
		t.Fatal("desired geometry preclaimed service success")
	}
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 2, H: 1})
	if len(s.TakePendingCommands()) != 1 {
		t.Fatal("unacknowledged size not retried")
	}
	p := s.projection
	p.Grid.Cols = 8
	p.Grid.GridRows = 2
	p.Generation++
	s.SetProjection(p)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 2, H: 1})
	if len(s.TakePendingCommands()) != 0 {
		t.Fatal("committed geometry repeated")
	}
}
func TestObserverLayoutNeverResizes(t *testing.T) {
	s, _ := newTestScreen(t)
	s.SetControlled(false)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 20, H: 5})
	if len(s.TakePendingCommands()) != 0 {
		t.Fatal("observer resized")
	}
}
