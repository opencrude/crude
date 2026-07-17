package compositor

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
)

func TestGenerateANSI_Empty(t *testing.T) {
	b := surface.NewCellBuffer(0, 0)
	if GenerateANSI(b) != "" {
		t.Fatal("empty buffer")
	}
	b2 := surface.NewCellBuffer(0, 5)
	if GenerateANSI(b2) != "" {
		t.Fatal("zero width")
	}
}

func TestGenerateANSI_PlainText(t *testing.T) {
	b := surface.NewCellBuffer(3, 2)
	b.Set(0, 0, cell.NewCell('A', cell.NoColor, cell.NoColor, 0))
	b.Set(1, 0, cell.NewCell('B', cell.NoColor, cell.NoColor, 0))
	b.Set(2, 0, cell.NewCell('C', cell.NoColor, cell.NoColor, 0))
	b.Set(0, 1, cell.NewCell('D', cell.NoColor, cell.NoColor, 0))
	out := GenerateANSI(b)
	// Two rows separated by newline; trailing spaces for empty cells
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("lines=%d out=%q", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "ABC") {
		t.Fatalf("row0 %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "D") {
		t.Fatalf("row1 %q", lines[1])
	}
}

func TestGenerateANSI_StyleChange(t *testing.T) {
	b := surface.NewCellBuffer(2, 1)
	b.Set(0, 0, cell.NewCell('R', cell.Red, cell.NoColor, cell.AttrBold))
	b.Set(1, 0, cell.NewCell('G', cell.Green, cell.NoColor, 0))
	out := GenerateANSI(b)
	if !strings.Contains(out, "R") || !strings.Contains(out, "G") {
		t.Fatalf("%q", out)
	}
	// Should contain SGR sequences
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected SGR in %q", out)
	}
	// Bold red uses 38;5;1 for palette red
	if !strings.Contains(out, "38;5;1") && !strings.Contains(out, ";1") {
		t.Fatalf("expected color/bold codes: %q", out)
	}
}

func TestGenerateANSI_TrueColorAndAttrs(t *testing.T) {
	b := surface.NewCellBuffer(1, 1)
	b.Set(0, 0, cell.NewCell('X',
		cell.NewTrueColor(10, 20, 30),
		cell.NewTrueColor(40, 50, 60),
		cell.AttrItalic|cell.AttrUnderline,
	))
	out := GenerateANSI(b)
	if !strings.Contains(out, "38;2;10;20;30") {
		t.Fatalf("fg truecolor missing: %q", out)
	}
	if !strings.Contains(out, "48;2;40;50;60") {
		t.Fatalf("bg truecolor missing: %q", out)
	}
	if !strings.Contains(out, ";3") || !strings.Contains(out, ";4") {
		t.Fatalf("attrs missing: %q", out)
	}
}

func TestGenerateANSI_SkipsContinuation(t *testing.T) {
	b := surface.NewCellBuffer(2, 1)
	b.Set(0, 0, cell.NewWideCell('中', cell.NoColor, cell.NoColor, 0))
	b.Set(1, 0, cell.Cell{Width: 0})
	out := GenerateANSI(b)
	// Only one 中, not doubled
	if strings.Count(out, "中") != 1 {
		t.Fatalf("continuation should be skipped: %q", out)
	}
}

func TestGenerateANSI_ZeroCharAsSpace(t *testing.T) {
	b := surface.NewCellBuffer(1, 1)
	b.Set(0, 0, cell.Cell{Char: 0, Width: 1})
	out := GenerateANSI(b)
	if !strings.Contains(out, " ") {
		t.Fatalf("zero char -> space: %q", out)
	}
}

func TestGenerateANSI_RowReset(t *testing.T) {
	b := surface.NewCellBuffer(1, 2)
	b.Set(0, 0, cell.NewCell('A', cell.Red, cell.NoColor, 0))
	b.Set(0, 1, cell.NewCell('B', cell.NoColor, cell.NoColor, 0))
	out := GenerateANSI(b)
	// End of styled row should reset before next row
	if !strings.Contains(out, "\x1b[0m") {
		t.Fatalf("expected row reset: %q", out)
	}
}

func TestGenerateDiffANSI(t *testing.T) {
	if GenerateDiffANSI(10, 5, nil) != "" {
		t.Fatal("nil diff")
	}
	if GenerateDiffANSI(10, 5, &DiffResult{}) != "" {
		t.Fatal("empty diff")
	}
	diff := &DiffResult{
		Cells: []CellChange{
			{X: 2, Y: 3, Cell: cell.NewCell('Z', cell.Red, cell.NoColor, 0)},
			{X: 0, Y: 0, Cell: cell.Cell{Width: 0}}, // skipped continuation
		},
	}
	out := GenerateDiffANSI(10, 5, diff)
	if !strings.Contains(out, "\x1b[4;3H") { // 1-based: y+1=4, x+1=3
		t.Fatalf("cursor pos missing: %q", out)
	}
	if !strings.Contains(out, "Z") {
		t.Fatalf("char missing: %q", out)
	}
}
