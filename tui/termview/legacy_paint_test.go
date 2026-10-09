package termview

import (
	"testing"

	"crude/terminalvt"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestLegacyInverseAndSelectionInk(t *testing.T) {
	var v View
	g := terminalvt.Projection{Cols: 8, GridRows: 2, Total: 2, Cells: [][]terminalvt.Cell{{{Text: "X", Width: 1, Style: terminalvt.Style{Inverse: true, Faint: true, Bold: true, Italic: true, Underline: true}}}, {}}}
	v.Install(g, 1)
	buf := surface.NewCellBuffer(8, 2)
	dark := ui.NewTokenSet(theme.DefaultDark())
	v.Paint(buf, 0, 0, 8, 2, dark, true, true)
	original := buf.Get(0, 0)
	if original.Attr&cell.AttrReverse == 0 || original.FG != dark.Color(ui.ColorFg) || original.BG != dark.Color(ui.ColorBg) {
		t.Fatalf("inverse pre-swapped=%+v", original)
	}
	v.SelectionPress(0, 0, 1_000_000_000)
	if v.HasSelection() {
		t.Fatal("single press selected")
	}
	v.SelectionDrag(0, 0)
	if !v.SelectionRelease(0, 0) {
		t.Fatal("release lost range")
	}
	for _, tokens := range []*ui.TokenSet{dark, ui.NewTokenSet(theme.DefaultLight())} {
		v.Paint(buf, 0, 0, 8, 2, tokens, true, true)
		got := buf.Get(0, 0)
		if got.FG != tokens.Color(ui.ColorTextSelectionFg) || got.BG != tokens.Color(ui.ColorTextSelection) || got.Attr != original.Attr&^(cell.AttrReverse|cell.AttrDim) {
			t.Fatalf("selected=%+v", got)
		}
		if copied, ok := v.SelectionText(); !ok || copied != "X" {
			t.Fatalf("copy=%q ok=%v", copied, ok)
		}
	}
	v.ClearSelection()
	v.Paint(buf, 0, 0, 8, 2, dark, true, true)
	if got := buf.Get(0, 0); got != original {
		t.Fatalf("clear changed child=%+v want=%+v", got, original)
	}
}

func TestLegacySelectedWideCursorAndClipping(t *testing.T) {
	for _, end := range []int{1, 2} {
		var v View
		v.Install(terminalvt.Projection{Cols: 8, GridRows: 2, Total: 2, Cells: [][]terminalvt.Cell{{{Text: "a", Width: 1}, {Text: "界", Width: 2}, {Width: 0}, {Text: "b", Width: 1}}, {}}, Cursor: terminalvt.Cursor{X: 2, Visible: true}}, 1)
		v.SelectionPress(0, 0, 1_000_000_000)
		v.SelectionDrag(end, 0)
		tokens := ui.NewTokenSet(theme.DefaultDark())
		buf := surface.NewCellBuffer(10, 4)
		v.Paint(buf, 1, 1, 8, 2, tokens, true, true)
		for x := 2; x <= 3; x++ {
			c := buf.Get(x, 1)
			if c.BG != tokens.Color(ui.ColorTextSelection) || c.Attr&cell.AttrReverse == 0 {
				t.Fatalf("selected cursor column=%d cell=%+v", x, c)
			}
		}
		if buf.Get(2, 1).Content != "界" || buf.Get(2, 1).Width != 2 || buf.Get(3, 1).Width != 0 || buf.Get(4, 1).BG != tokens.Color(ui.ColorBg) {
			t.Fatal("wide cursor damaged geometry or neighbor")
		}
		if text, ok := v.SelectionText(); !ok || text != "a界" {
			t.Fatalf("wide copy=%q ok=%v", text, ok)
		}
		clipped := surface.NewCellBuffer(2, 2)
		v.Paint(clipped, 0, 0, 2, 2, tokens, true, true)
		if clipped.Get(1, 0).Content == "界" {
			t.Fatal("paint emitted half-wide glyph at edge")
		}
	}
}

func TestLegacyCursorFocusBlinkAndHistoryPaint(t *testing.T) {
	var v View
	g := terminalvt.Projection{Cols: 8, GridRows: 2, Total: 3, Cells: [][]terminalvt.Cell{{{Text: "old", Width: 1}}, {{Text: "x", Width: 1}}, {}}, Cursor: terminalvt.Cursor{Visible: true, Blink: true}}
	v.Install(g, 1)
	tokens := ui.NewTokenSet(theme.DefaultDark())
	buf := surface.NewCellBuffer(8, 2)
	for _, tc := range []struct {
		focus, blink bool
		attrs        cell.Attrs
	}{{true, true, cell.AttrReverse}, {true, false, 0}, {false, false, cell.AttrReverse | cell.AttrDim}} {
		v.Paint(buf, 0, 0, 8, 2, tokens, tc.focus, tc.blink)
		c := buf.Get(0, 0)
		if c.Content != "x" || c.Attr != tc.attrs || c.BG != tokens.Color(ui.ColorBg) {
			t.Fatalf("focus=%v blink=%v cell=%+v", tc.focus, tc.blink, c)
		}
	}
	v.ScrollTop()
	v.Paint(buf, 0, 0, 8, 2, tokens, true, true)
	if c := buf.Get(0, 0); c.Content != "old" || c.Attr&cell.AttrReverse != 0 {
		t.Fatalf("history cursor=%+v", c)
	}
	v.ScrollBottom()
	if !v.Pinned() {
		t.Fatal("bottom did not rearm follow")
	}
}
