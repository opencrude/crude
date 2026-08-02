package uitest

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
)

func TestGoldenHashDeterministic(t *testing.T) {
	buf := surface.NewCellBuffer(4, 2)
	buf.Set(1, 1, cell.NewCell('x', cell.Red, cell.Blue, cell.AttrBold))
	h1 := GoldenHash(buf)
	h2 := GoldenHash(buf)
	if h1 != h2 {
		t.Fatalf("same buffer hashed differently: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("expected sha256 hex (64 chars), got %d", len(h1))
	}
}

func TestGoldenHashSensitiveToAnyCell(t *testing.T) {
	base := surface.NewCellBuffer(4, 2)
	base.Set(1, 1, cell.NewCell('x', cell.Red, cell.Blue, cell.AttrBold))
	baseHash := GoldenHash(base)

	// Every property of a cell must change the hash.
	variants := []func(buf *surface.CellBuffer){
		func(buf *surface.CellBuffer) { buf.Set(1, 1, cell.NewCell('y', cell.Red, cell.Blue, cell.AttrBold)) },
		func(buf *surface.CellBuffer) { buf.Set(1, 1, cell.NewCell('x', cell.Green, cell.Blue, cell.AttrBold)) },
		func(buf *surface.CellBuffer) { buf.Set(1, 1, cell.NewCell('x', cell.Red, cell.Green, cell.AttrBold)) },
		func(buf *surface.CellBuffer) { buf.Set(1, 1, cell.NewCell('x', cell.Red, cell.Blue, cell.AttrItalic)) },
		func(buf *surface.CellBuffer) { buf.Set(2, 1, cell.NewCell('x', cell.Red, cell.Blue, cell.AttrBold)) }, // shifted
	}
	for i, mutate := range variants {
		buf := surface.NewCellBuffer(4, 2)
		buf.Set(1, 1, cell.NewCell('x', cell.Red, cell.Blue, cell.AttrBold))
		mutate(buf)
		if got := GoldenHash(buf); got == baseHash {
			t.Fatalf("variant %d did not change the golden hash", i)
		}
	}
}
