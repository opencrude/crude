package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func tableRowText(buf *surface.CellBuffer, y, x, w int) string {
	var b strings.Builder
	for i := 0; i < w; i++ {
		c := buf.Get(x+i, y)
		if c.Char == 0 {
			break
		}
		b.WriteRune(c.Char)
	}
	return b.String()
}

func newTestTable(rows int) *Table {
	ts := NewTokenSet(theme.DefaultDark())
	tb := NewTable("t", ts)
	tb.SetColumns([]TableColumn{
		{ID: "name", Label: "Name", Track: FixedGridTrack(6)},
		{ID: "state", Label: "State", Track: FractionGridTrack(1)},
	})
	tb.SetRows(makeTestRows(rows))
	return tb
}

func makeTestRows(n int) []TableRow {
	rows := make([]TableRow, n)
	for i := range rows {
		rows[i] = TableRow{
			ID:    string(rune('a' + i%26)),
			Cells: []TableCell{{Text: "row"}, {Text: "ok"}},
		}
	}
	return rows
}

func TestTableColumnGapKeepsTruncatedLabelsOffNextColumn(t *testing.T) {
	tb := NewTable("t", NewTokenSet(theme.DefaultDark()))
	tb.SetColumnGap(2)
	tb.SetColumns([]TableColumn{{ID: "name", Label: "Account", Track: FractionGridTrack(3)}, {ID: "template", Label: "Template", Track: FractionGridTrack(2)}})
	tb.SetRows([]TableRow{{ID: "one", Cells: []TableCell{{Text: "a very long account label"}, {Text: "Codex"}}}})
	tb.SetShowHeader(true)
	tb.Layout(LayoutContext{}, Rect{W: 22, H: 3})
	if gap := tb.colX[1] - tb.colX[0] - tb.widths[0]; gap != 2 {
		t.Fatalf("column gap=%d want 2", gap)
	}
	buf := surface.NewCellBuffer(22, 3)
	tb.Paint(buf, 0, 0, PaintContext{})
	for x := tb.colX[0] + tb.widths[0]; x < tb.colX[1]; x++ {
		if char := buf.Get(x, 1).Char; char != ' ' && char != 0 {
			t.Fatalf("account label crossed gutter at x=%d: %q", x, char)
		}
	}
	if got := tableRowText(buf, 1, tb.colX[1], tb.widths[1]); !strings.Contains(got, "Codex") {
		t.Fatalf("template text lost after gap: %q", got)
	}
}

func TestTable_PageHomeEnd(t *testing.T) {
	tb := newTestTable(10)
	tb.Layout(LayoutContext{}, Rect{W: 20, H: 4})
	tb.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyPgDown}))
	if tb.SelectedIndex() != 4 {
		t.Fatalf("pgdown selected=%d want 4", tb.SelectedIndex())
	}
	if off := tb.Scroll().Offset(); off != 1 {
		t.Fatalf("pgdown ensure-visible offset=%d want 1", off)
	}
	tb.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyPgUp}))
	if tb.SelectedIndex() != 0 || tb.Scroll().Offset() != 0 {
		t.Fatalf("pgup selected=%d off=%d want 0/0", tb.SelectedIndex(), tb.Scroll().Offset())
	}
	tb.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnd}))
	if tb.SelectedIndex() != 9 {
		t.Fatalf("end selected=%d want 9", tb.SelectedIndex())
	}
	if off := tb.Scroll().Offset(); off != 6 {
		t.Fatalf("end ensure-visible offset=%d want 6 (9-4+1)", off)
	}
	tb.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyHome}))
	if tb.SelectedIndex() != 0 || tb.Scroll().Offset() != 0 {
		t.Fatalf("home selected=%d off=%d want 0/0", tb.SelectedIndex(), tb.Scroll().Offset())
	}
}

func TestTable_EnsureVisible(t *testing.T) {
	tb := newTestTable(20)
	tb.Layout(LayoutContext{}, Rect{W: 20, H: 3})
	if !tb.SelectIndex(17) {
		t.Fatal("select 17")
	}
	if off := tb.Scroll().Offset(); off != 15 {
		t.Fatalf("ensure-visible offset=%d want 15 (17-3+1)", off)
	}
	tb.SelectIndex(15)
	if off := tb.Scroll().Offset(); off != 15 {
		t.Fatalf("visible selection moved offset to %d", off)
	}
	tb.SelectIndex(2)
	if off := tb.Scroll().Offset(); off != 2 {
		t.Fatalf("scroll up to 2 got %d", off)
	}
}

func TestTable_KeyboardActivate(t *testing.T) {
	tb := newTestTable(3)
	tb.Layout(LayoutContext{}, Rect{W: 30, H: 5})
	tb.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	res := tb.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	row, ok := res.Action.Payload.(TableRow)
	if !ok || row.ID != "b" {
		t.Fatalf("activate payload=%+v", res.Action)
	}
	if res.Action.ID != "table.activate" {
		t.Fatalf("action id=%s", res.Action.ID)
	}
}

func TestTable_MouseAndWheel(t *testing.T) {
	tb := newTestTable(20)
	tb.Layout(LayoutContext{}, Rect{W: 20, H: 5})
	res := tb.Handle(EventContext{LocalY: 2}, MouseClickEvent(tea.MouseClickMsg{X: 0, Y: 2, Button: tea.MouseLeft}))
	if res.Action == nil || res.Action.ID != "table.activate" {
		t.Fatalf("click activate=%+v", res.Action)
	}
	if tb.SelectedIndex() != 2 {
		t.Fatalf("click selected=%d", tb.SelectedIndex())
	}
	off := tb.Scroll().Offset()
	tb.Handle(EventContext{}, MouseWheelEvent(tea.MouseWheelMsg{Button: tea.MouseWheelDown}))
	if tb.Scroll().Offset() <= off {
		t.Fatal("wheel must increase offset")
	}
}

func TestTable_StateDistinction(t *testing.T) {
	ts := NewTokenSet(theme.DefaultDark())
	tb := newTestTable(3)
	tb.Layout(LayoutContext{}, Rect{W: 20, H: 5})
	tb.SelectIndex(0)

	fbuf := surface.NewCellBuffer(20, 5)
	tb.Paint(fbuf, 0, 0, PaintContext{Focused: true})
	focusBG := fbuf.Get(1, 0).BG

	// Hover must not move selection nor activate.
	hb := surface.NewCellBuffer(20, 5)
	tb.Paint(hb, 0, 0, PaintContext{Focused: false})
	selBG := hb.Get(1, 0).BG

	res := tb.Handle(EventContext{LocalY: 1}, MouseMotionEvent(tea.MouseMotionMsg{}))
	if res.Action != nil {
		t.Fatal("hover must not activate")
	}
	if tb.SelectedIndex() != 0 {
		t.Fatal("hover must not move selection")
	}
	tb.Paint(hb, 0, 0, PaintContext{Focused: false})
	hoverBG := hb.Get(1, 1).BG

	if focusBG.Equal(selBG) || selBG.Equal(hoverBG) || focusBG.Equal(hoverBG) {
		t.Fatalf("states not distinct: focus=%v sel=%v hover=%v", focusBG, selBG, hoverBG)
	}
	if !focusBG.Equal(ts.Color(ColorAccent)) {
		t.Fatal("focused row should use Accent bg")
	}
	if !selBG.Equal(ts.Color(ColorSelection)) {
		t.Fatal("selected (unfocused) row should use Selection bg")
	}
	if !hoverBG.Equal(ts.Color(ColorHover)) {
		t.Fatal("hovered row should use Hover bg")
	}

	// Activation remains a separate step from selection: click activates.
	act := tb.Handle(EventContext{LocalY: 2}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if act.Action == nil || act.Action.ID != "table.activate" {
		t.Fatal("click must activate")
	}
}

func TestTable_PaintHeaderAndCells(t *testing.T) {
	ts := NewTokenSet(theme.DefaultDark())
	tb := NewTable("t", ts)
	tb.SetColumns([]TableColumn{
		{ID: "name", Label: "Name", Track: FixedGridTrack(6)},
		{ID: "state", Label: "State", Track: FixedGridTrack(5)},
	})
	tb.SetShowHeader(true)
	tb.SetRows([]TableRow{
		{ID: "r1", Cells: []TableCell{{Text: "alpha"}, {Text: "ok"}}},
		{ID: "r2", Cells: []TableCell{{Text: "beta"}, {Text: "warn"}}},
	})
	tb.Layout(LayoutContext{}, Rect{W: 30, H: 4})
	buf := surface.NewCellBuffer(30, 4)
	tb.Paint(buf, 0, 0, PaintContext{Focused: true})
	if got := tableRowText(buf, 0, 0, 4); got != "Name" {
		t.Fatalf("header col0=%q", got)
	}
	if got := tableRowText(buf, 0, 6, 5); got != "State" {
		t.Fatalf("header col1=%q", got)
	}
	if got := tableRowText(buf, 1, 0, 5); got != "alpha" {
		t.Fatalf("row0 col0=%q", got)
	}
	if got := tableRowText(buf, 1, 6, 2); got != "ok" {
		t.Fatalf("row0 col1=%q", got)
	}
}

func TestTable_ColumnWidths(t *testing.T) {
	ts := NewTokenSet(theme.DefaultDark())
	tb := NewTable("t", ts)
	tb.SetColumns([]TableColumn{
		{ID: "id", Track: FixedGridTrack(5)},
		{ID: "grow", Track: FractionGridTrack(1)},
		{ID: "auto", Track: GridTrack{Kind: GridTrackAuto}},
	})
	tb.SetShowHeader(true)
	tb.SetRows([]TableRow{
		{Cells: []TableCell{{Text: "x"}, {Text: "y"}, {Text: "wide column"}}},
	})
	tb.Layout(LayoutContext{}, Rect{W: 40, H: 5})
	autoW := 11 // visible width of "wide column"
	want := []int{5, 40 - 5 - autoW, autoW}
	if got := tb.widths; got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("widths=%v want %v", got, want)
	}
	if tb.colX[0] != 0 || tb.colX[1] != 5 || tb.colX[2] != 5+want[1] {
		t.Fatalf("colX=%v", tb.colX)
	}
}
