package surface

import (
	"strings"
	"testing"

	"crude/tui/bidi"
	"crude/tui/cell"
)

func rowText(b *CellBuffer, y int) string {
	var out strings.Builder
	for _, c := range b.Row(y) {
		switch {
		case c.Width == 0:
		case c.Content != "":
			out.WriteString(c.Content)
		case c.Char == 0:
			out.WriteByte(' ')
		default:
			out.WriteRune(c.Char)
		}
	}
	return out.String()
}

func TestWriteStringArrangesRightToLeftLabel(t *testing.T) {
	b := NewCellBuffer(12, 1)
	end := b.WriteString(1, 0, "שלום (1)", cell.NoColor, cell.NoColor, 0)
	if end != 9 {
		t.Fatalf("WriteString returned %d, want 9", end)
	}
	// An automatic right-to-left label: the number and its parentheses keep
	// reading order, the brackets take their mirrored glyphs.
	if got := strings.TrimSpace(rowText(b, 0)); got != "(1) םולש" {
		t.Fatalf("painted %q", got)
	}
	if got := strings.TrimSpace(ReadLogical(b.Row(0))); got != "שלום (1)" {
		t.Fatalf("logical read %q", got)
	}
}

func TestWriteStringJoinsPersian(t *testing.T) {
	b := NewCellBuffer(4, 1)
	b.WriteString(0, 0, "سلام", cell.NoColor, cell.NoColor, 0)
	// Visual order: meem (isolated), alef (final), lam (medial), seen (initial).
	if got := rowText(b, 0); got != "\ufee1\ufe8e\ufee0\ufeb3" {
		t.Fatalf("painted %q", got)
	}
	if got := ReadLogical(b.Row(0)); got != "سلام" {
		t.Fatalf("logical read %q", got)
	}
}

func TestWriteLogicalKeepsLogicalOrder(t *testing.T) {
	b := NewCellBuffer(4, 1)
	b.WriteLogical(0, 0, "שלום", cell.NoColor, cell.NoColor, 0)
	if got := rowText(b, 0); got != "שלום" {
		t.Fatalf("WriteLogical arranged text: %q", got)
	}
}

func TestLeftToRightLabelUntouched(t *testing.T) {
	b := NewCellBuffer(10, 1)
	b.WriteString(0, 0, "abc 日本", cell.NoColor, cell.NoColor, 0)
	for _, c := range b.Row(0) {
		if c.Bidi != 0 {
			t.Fatal("a left-to-right label got a bidi mark")
		}
	}
}

func TestApplyLineCarriesStyleWithCharacters(t *testing.T) {
	b := NewCellBuffer(9, 1)
	red := cell.NewTrueColor(255, 0, 0)
	// "ab אבג": the selection covers logical א and ב.
	b.WriteLogical(0, 0, "ab א", cell.NoColor, cell.NoColor, 0)
	b.WriteLogical(4, 0, "ב", cell.NoColor, red, 0)
	b.WriteLogical(5, 0, "ג", cell.NoColor, cell.NoColor, 0)
	b.cells[3].BG = red
	l := bidi.AnalyzeString("ab אבג", bidi.LeftToRight, false)
	if !b.ApplyLine(0, 0, l.Line(0, 6, 9)) {
		t.Fatal("ApplyLine refused a valid line")
	}
	if got := rowText(b, 0); got != "ab גבא   " {
		t.Fatalf("painted %q", got)
	}
	// Visual columns 4 (ב) and 5 (א) carry the selection; ג at 3 does not.
	if !b.cells[4].BG.Equal(red) || !b.cells[5].BG.Equal(red) || b.cells[3].BG.Equal(red) {
		t.Fatal("selection colors did not travel with their characters")
	}
}

func TestApplyLineRightAlignsRightToLeftParagraph(t *testing.T) {
	b := NewCellBuffer(8, 1)
	b.WriteLogical(0, 0, "אב", cell.NoColor, cell.NoColor, 0)
	// A block cursor on the end-of-text cell (logical column 2).
	b.Set(2, 0, cell.Cell{Char: ' ', Width: 1, Attr: cell.AttrReverse})
	l := bidi.AnalyzeString("אב", bidi.Auto, false)
	b.ApplyLine(0, 0, l.Line(0, 2, 8))
	if got := rowText(b, 0); got != "      בא" {
		t.Fatalf("painted %q", got)
	}
	if !b.cells[5].Attr.Has(cell.AttrReverse) {
		t.Fatal("the end-of-text cursor is not next to the text's left end")
	}
}

func TestApplyLineRefusesToSplitWideGrapheme(t *testing.T) {
	b := NewCellBuffer(4, 1)
	b.WriteLogical(0, 0, "日א", cell.NoColor, cell.NoColor, 0)
	// A line whose units disagree with the painted cells (a one-column unit
	// on the wide grapheme) must not move half of it.
	units := []bidi.Unit{{Text: "x", Width: 1}, {Text: "y", Width: 1}, {Text: "א", Width: 1}}
	l := bidi.Analyze(units, bidi.RightToLeft, false)
	before := append([]cell.Cell(nil), b.Row(0)...)
	if b.ApplyLine(0, 0, l.Line(0, 3, 3)) {
		t.Fatal("ApplyLine split a wide grapheme")
	}
	for i := range before {
		if before[i] != b.Row(0)[i] {
			t.Fatal("a refused line changed cells")
		}
	}
}

func TestReadLogicalKeepsSeparateLabelsApart(t *testing.T) {
	b := NewCellBuffer(10, 1)
	b.WriteString(0, 0, "אב", cell.NoColor, cell.NoColor, 0)
	b.WriteString(3, 0, "גד", cell.NoColor, cell.NoColor, 0)
	if got := ReadLogical(b.Row(0)); strings.TrimSpace(got) != "אב גד" {
		t.Fatalf("logical read %q", got)
	}
}

// The label path must cost nothing measurable for left-to-right text: it
// runs for every label of every frame.
func BenchmarkWriteStringLeftToRight(b *testing.B) {
	buf := NewCellBuffer(120, 1)
	text := "internal/workspace/session_manager.go  ✓ 3 files changed · 日本語"
	b.ReportAllocs()
	for b.Loop() {
		buf.WriteString(0, 0, text, cell.NoColor, cell.NoColor, 0)
	}
}

func BenchmarkWriteStringRightToLeft(b *testing.B) {
	buf := NewCellBuffer(120, 1)
	text := "سلام دنیا، این یک پیام آزمایشی است (۱۲۳) with English"
	b.ReportAllocs()
	for b.Loop() {
		buf.WriteString(0, 0, text, cell.NoColor, cell.NoColor, 0)
	}
}
