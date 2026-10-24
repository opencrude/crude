package shellterm

import (
	"reflect"
	"testing"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/terminalvt"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func TestImmutableSDKProjectionPaintCopyAndNextFramePreserveSource(t *testing.T) {
	row := func(text string, wrapped bool) []byte {
		cells := make([]terminalvt.Cell, 8)
		for x := range cells {
			cells[x] = terminalvt.Cell{Text: " ", Width: 1}
			if x < len(text) {
				cells[x].Text = text[x : x+1]
			}
		}
		return terminalvt.AppendRow(nil, cells, wrapped)
	}
	frame := coreprotocol.TerminalFrame{Projection: workspace.TerminalProjection{Info: workspace.TerminalInfo{ID: "sdk", Alive: true}, Generation: 1, Grid: terminalvt.Projection{Cols: 8, GridRows: 2, Total: 2}}, Rows: []coreprotocol.TerminalRow{{Index: 0, Data: row("hello   ", true)}, {Index: 1, Data: row("world", false)}}}
	var grid coreclient.TerminalGrid
	p, err := grid.Apply(coreprotocol.TopicHeader{Seq: 1, Reset: true}, frame)
	if err != nil {
		t.Fatal(err)
	}
	want := make([][]terminalvt.Cell, len(p.Grid.Cells))
	for y, cells := range p.Grid.Cells {
		want[y] = append([]terminalvt.Cell(nil), cells...)
	}
	s := New(theme.DefaultDark(), "sdk", "Terminal")
	defer s.Close()
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 8, H: 2})
	s.SetImmutableProjection(p)
	if &s.view.Grid.Cells[0][0] != &p.Grid.Cells[0][0] {
		t.Fatal("SDK-to-screen publication cloned immutable cells")
	}
	s.view.SelectAll()
	if text, ok := s.view.SelectionText(); !ok || text != "hello   world" {
		t.Fatalf("SDK wraps/copy lost: %q %v", text, ok)
	}
	buf := surface.NewCellBuffer(8, 2)
	s.Paint(buf, 0, 0, ui.PaintContext{})
	if buf.Get(0, 0).Content != "h" {
		t.Fatal("SDK page did not reach rendered screen")
	}
	frame.Base, frame.Projection.Generation = 1, 2
	frame.Rows = []coreprotocol.TerminalRow{{Index: 0, Data: row("changed", false)}}
	next, err := grid.Apply(coreprotocol.TopicHeader{Seq: 2}, frame)
	if err != nil {
		t.Fatal(err)
	}
	s.SetImmutableProjection(next)
	s.view.ClearSelection()
	s.Paint(buf, 0, 0, ui.PaintContext{})
	if buf.Get(0, 0).Content != "c" || !reflect.DeepEqual(p.Grid.Cells, want) {
		t.Fatal("next SDK publication failed to paint or mutated previous source")
	}
}
