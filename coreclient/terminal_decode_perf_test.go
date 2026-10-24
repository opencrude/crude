package coreclient

import (
	"reflect"
	"testing"

	"crude/coreprotocol"
	"crude/terminalvt"
	"crude/workspace"
)

// Every cell changes color between consecutive full 200x50 frames. Short style
// runs remain inline, matching truecolor terminal floods rather than a repeated
// plain-text row or a dictionary of a million cached colors.
func terminalColorFloodFrames() []coreprotocol.TerminalFrame {
	frames := make([]coreprotocol.TerminalFrame, 4)
	var encoder terminalvt.RowEncoder
	for n := range frames {
		frame := &frames[n]
		frame.Projection = workspace.TerminalProjection{Grid: terminalvt.Projection{Cols: 200, GridRows: 50, Total: 50}}
		for y := range 50 {
			cells := make([]terminalvt.Cell, 200)
			for x := range cells {
				color := uint32(n*10000 + y*200 + x)
				cells[x] = terminalvt.Cell{Text: "x", Width: 1, FG: terminalvt.Color{Explicit: true, R: byte(color >> 16), G: byte(color >> 8), B: byte(color)}}
			}
			frame.Rows = append(frame.Rows, coreprotocol.TerminalRow{Index: y, Data: encoder.Append(nil, cells, y%2 == 0)})
		}
		frame.StyleBase = len(encoder.Definitions)
	}
	// The encoder's default-style definition is unused by these inline runs.
	// Send it once on the initial reset, then retain the exact dictionary base.
	frames[0].StyleBase = 0
	frames[0].Styles = append([]terminalvt.Cell(nil), encoder.Definitions...)
	return frames
}

func BenchmarkTerminalGridColorFlood200x50(b *testing.B) {
	frames := terminalColorFloodFrames()
	var grid TerminalGrid
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		frame := frames[i%len(frames)]
		frame.Base = uint64(i)
		if i > 0 {
			frame.StyleBase, frame.Styles = len(grid.decoder.Styles), nil
		}
		if _, err := grid.Apply(coreprotocol.TopicHeader{Seq: uint64(i + 1), Reset: i == 0}, frame); err != nil {
			b.Fatal(err)
		}
	}
}

func TestTerminalGridColorFloodAllocatesOneBufferPerRow(t *testing.T) {
	frames := terminalColorFloodFrames()
	var grid TerminalGrid
	if _, err := grid.Apply(coreprotocol.TopicHeader{Seq: 1, Reset: true}, frames[0]); err != nil {
		t.Fatal(err)
	}
	seq := uint64(1)
	allocs := testing.AllocsPerRun(100, func() {
		frame := frames[seq%uint64(len(frames))]
		frame.Base, frame.StyleBase, frame.Styles = seq, len(grid.decoder.Styles), nil
		seq++
		if _, err := grid.Apply(coreprotocol.TopicHeader{Seq: seq}, frame); err != nil {
			t.Fatal(err)
		}
	})
	// Fifty immutable row buffers, plus Cells/Wrapped/seen. A nil Decode
	// destination grew nine times per row (453 allocations for this workload).
	if allocs > 53 {
		t.Fatalf("full 200x50 truecolor publication allocated %.0f times; want <=53", allocs)
	}
	for y, row := range grid.projection.Grid.Cells {
		for x, cell := range row {
			color := uint32(int((seq-1)%uint64(len(frames)))*10000 + y*200 + x)
			if cell.Text != "x" || cell.Width != 1 || cell.FG != (terminalvt.Color{Explicit: true, R: byte(color >> 16), G: byte(color >> 8), B: byte(color)}) {
				t.Fatalf("published cell %d,%d lost inline truecolor: %+v", x, y, cell)
			}
		}
	}
}

func TestTerminalGridPublicationSurvivesScrollRejectResizeAndStyleFault(t *testing.T) {
	row := func(text string) []byte {
		cells := make([]terminalvt.Cell, 8)
		for i := range cells {
			cells[i] = terminalvt.Cell{Text: text, Width: 1}
		}
		return terminalvt.AppendRow(nil, cells, text == "a")
	}
	frame := coreprotocol.TerminalFrame{Projection: workspace.TerminalProjection{Grid: terminalvt.Projection{Cols: 8, GridRows: 3, Total: 3}}, Rows: []coreprotocol.TerminalRow{{Index: 0, Data: row("a")}, {Index: 1, Data: row("b")}, {Index: 2, Data: row("c")}}}
	var grid TerminalGrid
	first, err := grid.Apply(coreprotocol.TopicHeader{Seq: 1, Reset: true}, frame)
	if err != nil {
		t.Fatal(err)
	}
	before := cloneTerminalPublication(first)
	frame.Base, frame.Scrolls = 1, []coreprotocol.TerminalScroll{{Top: 0, Bottom: 3, Rows: 1}}
	frame.Rows = []coreprotocol.TerminalRow{{Index: 2, Data: row("d")}}
	next, err := grid.Apply(coreprotocol.TopicHeader{Seq: 2}, frame)
	if err != nil {
		t.Fatal(err)
	}
	if next.Grid.Cells[0][0].Text != "b" || next.Grid.Cells[1][0].Text != "c" || next.Grid.Cells[2][0].Text != "d" || next.Grid.Wrapped[0] {
		t.Fatal("scroll changed row order/wrap metadata", next.Grid)
	}
	nextBefore := cloneTerminalPublication(next)
	frame.Base = 2
	frame.Rows = []coreprotocol.TerminalRow{{Index: 2, Data: []byte{255}}}
	if _, err := grid.Apply(coreprotocol.TopicHeader{Seq: 3}, frame); err == nil {
		t.Fatal("accepted malformed row after partial scroll")
	}
	frame.Scrolls = nil
	frame.Rows = []coreprotocol.TerminalRow{{Index: 0, Data: row("e")}}
	frame.Styles = []terminalvt.Cell{{Text: "invalid-style"}}
	if _, err := grid.Apply(coreprotocol.TopicHeader{Seq: 3}, frame); err == nil {
		t.Fatal("accepted malformed style definition")
	}
	frame.Styles = nil
	frame.Projection.Grid.Cols = terminalvt.MaxCols + 1
	if _, err := grid.Apply(coreprotocol.TopicHeader{Seq: 3, Reset: true}, frame); err == nil {
		t.Fatal("accepted unbounded grid width")
	}
	frame.Projection.Grid.Cols = 8
	frame.Projection.Grid.GridRows, frame.Projection.Grid.Total = 2, 2
	frame.Rows = []coreprotocol.TerminalRow{{Index: 0, Data: row("e")}, {Index: 1, Data: row("f")}}
	if _, err := grid.Apply(coreprotocol.TopicHeader{Seq: 3, Reset: true}, frame); err != nil {
		t.Fatal("rejections advanced committed sequence", err)
	}
	if !reflect.DeepEqual(first, before) || !reflect.DeepEqual(next, nextBefore) {
		t.Fatal("later scroll, rejection or resize mutated an immutable publication")
	}
}

func cloneTerminalPublication(p workspace.TerminalProjection) workspace.TerminalProjection {
	p.Grid.Cells = append([][]terminalvt.Cell(nil), p.Grid.Cells...)
	for i, row := range p.Grid.Cells {
		p.Grid.Cells[i] = append([]terminalvt.Cell(nil), row...)
	}
	p.Grid.Wrapped = append([]bool(nil), p.Grid.Wrapped...)
	return p
}
