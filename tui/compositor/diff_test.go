package compositor

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
)

func TestDiff_NoDamage(t *testing.T) {
	front := surface.NewCellBuffer(4, 2)
	back := surface.NewCellBuffer(4, 2)
	d := NewDamageRegion()
	res := Diff(front, back, d)
	if len(res.Cells) != 0 {
		t.Fatal("no damage")
	}
	if DiffCount(front, back, d) != 0 {
		t.Fatal("count 0")
	}
}

func TestDiff_ChangedCells(t *testing.T) {
	front := surface.NewCellBuffer(4, 2)
	back := surface.NewCellBuffer(4, 2)
	back.Set(1, 0, cell.NewCell('X', cell.Red, cell.Blue, 0))
	back.Set(2, 1, cell.NewCell('Y', cell.Green, cell.NoColor, 0))
	d := NewDamageRegion()
	d.MarkRect(surface.FullRect(4, 2))
	res := Diff(front, back, d)
	if len(res.Cells) != 2 {
		t.Fatalf("changes=%d %+v", len(res.Cells), res.Cells)
	}
	if DiffCount(front, back, d) != 2 {
		t.Fatal("count")
	}
}

func TestDiff_SkipsContinuation(t *testing.T) {
	front := surface.NewCellBuffer(4, 1)
	back := surface.NewCellBuffer(4, 1)
	back.Set(0, 0, cell.NewWideCell('中', cell.Red, cell.Blue, 0))
	back.Set(1, 0, cell.Cell{Width: 0, FG: cell.Red, BG: cell.Blue})
	d := NewDamageRegion()
	d.MarkRect(surface.FullRect(4, 1))
	res := Diff(front, back, d)
	// Only primary wide cell, not continuation
	if len(res.Cells) != 1 {
		t.Fatalf("want 1 change, got %d %+v", len(res.Cells), res.Cells)
	}
	if res.Cells[0].Cell.Char != '中' {
		t.Fatal(res.Cells[0])
	}
}

func TestDiff_IdenticalCellsIgnored(t *testing.T) {
	front := surface.NewCellBuffer(2, 1)
	back := surface.NewCellBuffer(2, 1)
	c := cell.NewCell('A', cell.Red, cell.Blue, 0)
	front.Set(0, 0, c)
	back.Set(0, 0, c)
	back.Set(1, 0, cell.NewCell('B', cell.NoColor, cell.NoColor, 0))
	d := NewDamageRegion()
	d.MarkRect(surface.FullRect(2, 1))
	res := Diff(front, back, d)
	if len(res.Cells) != 1 || res.Cells[0].Cell.Char != 'B' {
		t.Fatalf("%+v", res.Cells)
	}
}

func TestDiff_ClampsDamageBounds(t *testing.T) {
	front := surface.NewCellBuffer(2, 2)
	back := surface.NewCellBuffer(2, 2)
	back.Set(0, 0, cell.NewCell('Z', cell.NoColor, cell.NoColor, 0))
	d := NewDamageRegion()
	// Damage way outside buffer
	d.Mark(-10, -10)
	d.Mark(100, 100)
	res := Diff(front, back, d)
	// Should still find the change after clamp
	found := false
	for _, ch := range res.Cells {
		if ch.Cell.Char == 'Z' {
			found = true
		}
	}
	if !found {
		t.Fatalf("clamped diff missed change: %+v", res.Cells)
	}
}

func TestDiffRow(t *testing.T) {
	front := surface.NewCellBuffer(3, 2)
	back := surface.NewCellBuffer(3, 2)
	back.Set(1, 1, cell.NewCell('Q', cell.NoColor, cell.NoColor, 0))
	ch := DiffRow(front, back, 1, 0, 3)
	if len(ch) != 1 || ch[0].Y != 1 || ch[0].Cell.Char != 'Q' {
		t.Fatalf("%+v", ch)
	}
	if DiffRow(front, back, -1, 0, 3) != nil && len(DiffRow(front, back, -1, 0, 3)) != 0 {
		t.Fatal("oob row")
	}
	if len(DiffRow(front, back, 99, 0, 3)) != 0 {
		t.Fatal("oob high")
	}
}
