package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func rowString(buf *surface.CellBuffer, y int) string {
	var out strings.Builder
	for _, c := range buf.Row(y) {
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

func sourceRow(source *rendering.CopyText, start, end int) PaintLine {
	return PaintLine{SourceAware: true, Spans: []RowSpan{{Text: source.Value[start:end], Role: ui.ColorFg, Copy: rendering.CopyRange{Text: source, Start: start, End: end}}}}
}

func TestPersianParagraphRowsShareRightToLeftDirection(t *testing.T) {
	// One source paragraph wrapped into two rows; the second row starts
	// with an English word but belongs to the Persian paragraph.
	source := &rendering.CopyText{Value: "سلام دنیا API خوب"}
	split := strings.Index(source.Value, "API")
	tokens := ui.NewTokenSet(theme.DefaultDark())
	buf := surface.NewCellBuffer(12, 2)
	PaintLineAtBackground(buf, 0, 0, 12, sourceRow(source, 0, split-1), tokens, 0, 0, cell.NoColor)
	PaintLineAtBackground(buf, 0, 1, 12, sourceRow(source, split, len(source.Value)), tokens, 0, 0, cell.NoColor)
	if got := rowString(buf, 0); !strings.HasPrefix(got, "   ") || strings.HasSuffix(got, " ") {
		t.Fatalf("first row is not right-aligned: %q", got)
	}
	// "API خوب" in a right-to-left paragraph: the Persian word comes first,
	// at the right; the English word reads left to right on its left.
	got := strings.TrimLeft(rowString(buf, 1), " ")
	if !strings.HasPrefix(got, "\ufe8f\ufeee\ufea7 API") && !strings.HasSuffix(got, "API") {
		t.Fatalf("second row = %q", got)
	}
	if strings.Index(got, "API") < strings.Index(got, "\ufea7") {
		t.Fatalf("English word placed before the Persian word in an RTL row: %q", got)
	}
}

func TestCodeRowsStayLeftToRight(t *testing.T) {
	source := &rendering.CopyText{Value: "x := \"سلام\"", Code: true}
	ln := ArrangedLine(sourceRow(source, 0, len(source.Value)), 20)
	if ln == nil {
		t.Fatal("a code row with a Persian string was not arranged")
	}
	if ln.RTL() {
		t.Fatal("a code row took a right-to-left direction")
	}
	if ln.Visual(0) != 0 {
		t.Fatal("code text moved off the left edge")
	}
}

func TestLeftToRightRowsAreUntouched(t *testing.T) {
	source := &rendering.CopyText{Value: "plain English text"}
	if ArrangedLine(sourceRow(source, 0, len(source.Value)), 40) != nil {
		t.Fatal("an English row produced an arrangement")
	}
}

func TestLtrTableKeepsDividersOutsideBidi(t *testing.T) {
	th := theme.DefaultDark()
	rows, _ := renderMarkdownRows("| First | Second |\n|---|---|\n| שם│בית | قيمة |", rendering.MakeMdRenderer(50, th), ui.NewTokenSet(th), "m", false)
	if len(rows) < 3 {
		t.Fatal("table did not render")
	}
	for _, row := range rows {
		if !strings.Contains(row.PlainText(), "שם│בית") {
			continue
		}
		before := row.PlainText()
		border := rendering.VisibleWidth(before[:strings.LastIndex(before, "│")])
		literal := rendering.VisibleWidth(before[:strings.Index(before, "│")])
		mapped := ArrangedLine(row, 30)
		if mapped == nil || mapped.RTL() || mapped.Visual(border) != border {
			t.Fatalf("LTR table did not keep its border at column %d", border)
		}
		if mapped.Visual(literal-1) < literal {
			t.Fatalf("literal rail in the cell unexpectedly split its bidi paragraph: %q", before)
		}
		return
	}
	t.Fatal("table did not show both RTL cells")
}
