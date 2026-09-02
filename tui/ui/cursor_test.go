package ui

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
)

func TestPaintBlockCursorPreservesWideGraphemeSpan(t *testing.T) {
	buf := surface.NewCellBuffer(4, 1)
	buf.SetSpan(0, 0, cell.NewWideCell('界', cell.White, cell.Black, 0))
	buf.Set(2, 0, cell.NewCell('X', cell.White, cell.Black, 0))

	PaintBlockCursor(buf, 0, 0, cell.White, cell.Black)
	primary := buf.Get(0, 0)
	continuation := buf.Get(1, 0)
	if primary.Char != '界' || primary.Width != 2 || !primary.Attr.Has(cell.AttrReverse) {
		t.Fatalf("wide cursor primary=%+v", primary)
	}
	if continuation.Width != 0 || !continuation.Attr.Has(cell.AttrReverse) {
		t.Fatalf("wide cursor continuation=%+v", continuation)
	}
	if next := buf.Get(2, 0); next.Char != 'X' || next.Attr.Has(cell.AttrReverse) {
		t.Fatalf("cursor escaped grapheme span: %+v", next)
	}
}

func TestPaintBlockCursorAtEmptyCellDoesNotAdvanceContent(t *testing.T) {
	buf := surface.NewCellBuffer(4, 1)
	buf.Set(0, 0, cell.NewCell('a', cell.White, cell.Black, 0))
	buf.Set(1, 0, cell.NewCell('b', cell.White, cell.Black, 0))

	PaintBlockCursor(buf, 2, 0, cell.White, cell.Black)
	if c := buf.Get(2, 0); c.Char != ' ' || c.Width != 1 || !c.Attr.Has(cell.AttrReverse) {
		t.Fatalf("empty cursor cell=%+v", c)
	}
	if buf.Get(0, 0).Char != 'a' || buf.Get(1, 0).Char != 'b' {
		t.Fatal("empty cursor shifted existing content")
	}
}
