package termview

import (
	"strings"
	"testing"

	"crude/terminalvt"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func terminalRow(text string, cols int) []terminalvt.Cell {
	row := make([]terminalvt.Cell, 0, cols)
	for _, r := range text {
		row = append(row, terminalvt.Cell{Text: string(r), Width: 1})
	}
	for len(row) < cols {
		row = append(row, terminalvt.Cell{})
	}
	return row
}

func paintedRow(buf *surface.CellBuffer, y int) string {
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
	return strings.TrimRight(out.String(), " ")
}

func installRow(v *View, text string, alt bool) {
	const cols = 12
	v.Install(terminalvt.Projection{
		Cols: cols, GridRows: 1, Total: 1,
		Cells: [][]terminalvt.Cell{terminalRow(text, cols)},
		Modes: terminalvt.Modes{AltScreen: alt},
	}, 1)
}

// Shell output is arranged like a terminal in implicit BiDi mode: the row
// stays left to right and the Hebrew run reads right to left in place.
// Full-screen programs keep their own cells unless the user chose All
// output; As written never arranges. Pointer columns and copy address the
// program's own columns.
func TestTerminalRowsArrangeShellOutputOnly(t *testing.T) {
	defer SetArrangement(CurrentArrangement())
	tokens := ui.NewTokenSet(theme.DefaultDark())
	for _, tc := range []struct {
		arrangement Arrangement
		alt         bool
		want        string
	}{
		{ArrangeShell, false, "ab גבא cd"},
		{ArrangeShell, true, "ab אבג cd"},
		{ArrangeAll, true, "ab גבא cd"},
		{ArrangeNone, false, "ab אבג cd"},
	} {
		SetArrangement(tc.arrangement)
		var v View
		installRow(&v, "ab אבג cd", tc.alt)
		buf := surface.NewCellBuffer(12, 1)
		v.Paint(buf, 0, 0, 12, 1, tokens, false, false)
		if got := paintedRow(buf, 0); got != tc.want {
			t.Fatalf("arrangement %d alt=%v: row %q, want %q", tc.arrangement, tc.alt, got, tc.want)
		}
		arranged := tc.want != "ab אבג cd"
		// Screen column 3 shows ג when arranged (program column 5).
		if got, want := v.LogicalColumn(3, 0), map[bool]int{true: 5, false: 3}[arranged]; got != want {
			t.Fatalf("arrangement %d alt=%v: pointer column 3 maps to %d, want %d", tc.arrangement, tc.alt, got, want)
		}
	}
	SetArrangement(ArrangeShell)
	var v View
	installRow(&v, "ab אבג cd", false)
	v.SelectAll()
	if text, ok := v.SelectionText(); !ok || strings.TrimRight(text, " ") != "ab אבג cd" {
		t.Fatalf("copy = %q, want the program's text", text)
	}
}
