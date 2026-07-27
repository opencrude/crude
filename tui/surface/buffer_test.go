package surface

import (
	"testing"

	"crude/tui/cell"
)

func TestCellBuffer_NewClearDimensions(t *testing.T) {
	b := NewCellBuffer(4, 3)
	if b.Width() != 4 || b.Height() != 3 {
		t.Fatalf("dims %dx%d", b.Width(), b.Height())
	}
	if len(b.Cells()) != 12 {
		t.Fatalf("cells len %d", len(b.Cells()))
	}
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			c := b.Get(x, y)
			if !c.Transparent || c.Char != ' ' {
				t.Fatalf("expected transparent blank at %d,%d: %+v", x, y, c)
			}
		}
	}
}

func TestCellBuffer_SetGetOOB(t *testing.T) {
	b := NewCellBuffer(2, 2)
	ch := cell.NewCell('Z', cell.Red, cell.Blue, 0)
	b.Set(0, 0, ch)
	if !b.Get(0, 0).Equal(ch) {
		t.Fatal("set/get")
	}
	// OOB writes ignored
	b.Set(-1, 0, ch)
	b.Set(0, -1, ch)
	b.Set(2, 0, ch)
	b.Set(0, 2, ch)
	// OOB reads transparent blank
	oob := b.Get(-1, 0)
	if !oob.Transparent {
		t.Fatalf("OOB get: %+v", oob)
	}
}

func TestCellBuffer_FillAndFillRect(t *testing.T) {
	b := NewCellBuffer(5, 5)
	fill := cell.BlankCell(cell.Blue)
	b.Fill(fill)
	if !b.Get(4, 4).Equal(fill) {
		t.Fatal("fill whole")
	}
	red := cell.NewCell('r', cell.Red, cell.Red, 0)
	b.FillRect(Rect{X: 1, Y: 1, W: 2, H: 2}, red)
	if !b.Get(1, 1).Equal(red) || !b.Get(2, 2).Equal(red) {
		t.Fatal("fillrect interior")
	}
	if b.Get(0, 0).Equal(red) {
		t.Fatal("fillrect should not touch outside")
	}
	// OOB fillrect clipped
	b.FillRect(Rect{X: 4, Y: 4, W: 10, H: 10}, red)
	if !b.Get(4, 4).Equal(red) {
		t.Fatal("clipped fillrect")
	}
	// Empty after clip
	b.FillRect(Rect{X: 100, Y: 100, W: 1, H: 1}, red)
}

func TestCellBuffer_ResizePreserves(t *testing.T) {
	b := NewCellBuffer(3, 3)
	c := cell.NewCell('A', cell.Green, cell.NoColor, 0)
	b.Set(0, 0, c)
	b.Set(2, 2, c)
	b.Resize(5, 4)
	if b.Width() != 5 || b.Height() != 4 {
		t.Fatal("resize dims")
	}
	if !b.Get(0, 0).Equal(c) {
		t.Fatal("preserved 0,0")
	}
	// 2,2 was in old buffer — still there
	if !b.Get(2, 2).Equal(c) {
		t.Fatal("preserved 2,2")
	}
	// New cells transparent
	if !b.Get(4, 3).Transparent {
		t.Fatal("new cell should be transparent blank")
	}
	// Shrink
	b.Resize(1, 1)
	if !b.Get(0, 0).Equal(c) {
		t.Fatal("shrink preserve")
	}
	// No-op resize
	b.Resize(1, 1)
}

func TestCellBuffer_WriteString(t *testing.T) {
	b := NewCellBuffer(10, 2)
	end := b.WriteString(0, 0, "hi", cell.Red, cell.Blue, cell.AttrBold)
	if end != 2 {
		t.Fatalf("end x=%d", end)
	}
	if b.Get(0, 0).Char != 'h' || b.Get(1, 0).Char != 'i' {
		t.Fatal("ascii write")
	}
	if !b.Get(0, 0).FG.Equal(cell.Red) {
		t.Fatal("fg")
	}
	// OOB row
	if b.WriteString(0, 99, "x", cell.NoColor, cell.NoColor, 0) != 0 {
		t.Fatal("oob row should return x unchanged (0)")
	}
}

func TestCellBuffer_WriteString_WideChar(t *testing.T) {
	b := NewCellBuffer(4, 1)
	// CJK wide char 中 is width 2
	end := b.WriteString(0, 0, "中", cell.NoColor, cell.NoColor, 0)
	if end != 2 {
		t.Fatalf("wide end=%d", end)
	}
	c0 := b.Get(0, 0)
	if c0.Width != 2 || c0.Char != '中' {
		t.Fatalf("wide primary: %+v", c0)
	}
	if c0.Content != "中" {
		t.Fatalf("wide content=%q want original grapheme", c0.Content)
	}
	c1 := b.Get(1, 0)
	if c1.Width != 0 || c1.Char != 0 {
		t.Fatalf("continuation: %+v", c1)
	}
}

func TestCellBuffer_WriteString_PreservesGraphemeContent(t *testing.T) {
	b := NewCellBuffer(4, 1)
	cluster := "👩‍💻"
	end := b.WriteString(0, 0, cluster, cell.NoColor, cell.NoColor, 0)
	if end != 2 {
		t.Fatalf("grapheme end=%d want 2", end)
	}
	if got := b.Get(0, 0).Content; got != cluster {
		t.Fatalf("grapheme content=%q want %q", got, cluster)
	}
	if b.Get(0, 0).Width != 2 || b.Get(1, 0).Width != 0 {
		t.Fatalf("grapheme cells=%+v/%+v", b.Get(0, 0), b.Get(1, 0))
	}
}

func TestCellBuffer_WriteString_WideCharTruncatedAtEdge(t *testing.T) {
	b := NewCellBuffer(3, 1)
	// Place wide char at x=2 where only 1 col remains
	end := b.WriteString(2, 0, "中", cell.NoColor, cell.NoColor, 0)
	if end != 2 {
		t.Fatalf("truncated end should stay 2, got %d", end)
	}
	if b.Get(2, 0).Char == '中' {
		t.Fatal("wide char must not split at edge")
	}
}

func TestCellBuffer_Clone(t *testing.T) {
	b := NewCellBuffer(2, 2)
	b.Set(0, 0, cell.NewCell('x', cell.Red, cell.NoColor, 0))
	cl := b.Clone()
	if !cl.Get(0, 0).Equal(b.Get(0, 0)) {
		t.Fatal("clone content")
	}
	// Mutating clone does not affect original
	cl.Set(0, 0, cell.NewCell('y', cell.NoColor, cell.NoColor, 0))
	if b.Get(0, 0).Char != 'x' {
		t.Fatal("clone independence")
	}
}

func TestCellBuffer_Row(t *testing.T) {
	b := NewCellBuffer(3, 2)
	b.Set(1, 1, cell.NewCell('q', cell.NoColor, cell.NoColor, 0))
	row := b.Row(1)
	if len(row) != 3 || row[1].Char != 'q' {
		t.Fatalf("row: %v", row)
	}
	if b.Row(-1) != nil || b.Row(2) != nil {
		t.Fatal("oob row nil")
	}
}

func TestCellBuffer_Clear(t *testing.T) {
	b := NewCellBuffer(2, 2)
	b.Fill(cell.BlankCell(cell.Red))
	b.Clear()
	if !b.Get(0, 0).Transparent {
		t.Fatal("clear to transparent")
	}
}
