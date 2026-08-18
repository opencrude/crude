package ui

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestPlanCompactTableDistributesLeftoverWidth(t *testing.T) {
	plan := planCompactTable(80, 11, 1, []int{16, 11, 24})
	if plan.gapL < TableMinGap {
		t.Fatalf("label gap=%d, want at least %d", plan.gapL, TableMinGap)
	}
	if plan.flex < 1 {
		t.Fatal("wide rows must spend leftover width before the last column")
	}
	used := 11 + 16 + 11 + 24 + 2*rendering.VisibleWidth(TableSep) + 1 + plan.gapL + plan.flex
	if used != 80 {
		t.Fatalf("plan must consume the row: used=%d", used)
	}
}

func TestPlanCompactTableAlignsSharedColumns(t *testing.T) {
	cols := []int{16, 11, 24}
	a := planCompactTable(80, 11, 1, cols)
	b := planCompactTable(80, 11, 1, cols)
	if a.gapL != b.gapL || a.flex != b.flex {
		t.Fatalf("identical inputs must share a plan: %+v vs %+v", a, b)
	}
	if a.labelW+a.gapL != 11+a.gapL {
		t.Fatal("values must start after the shared label column")
	}
}

func TestPaintMenuDetailUsesMinimumGap(t *testing.T) {
	buf := surface.NewCellBuffer(20, 1)
	buf.Fill(cell.BlankCell(cell.NoColor))
	paintMenuDetail(buf, 0, 0, 20, "Save", "Ctrl+S", cell.White, cell.GrayMid, cell.NoColor, 0)
	labelEnd := rendering.VisibleWidth("Save")
	detailStart := -1
	for x := 0; x < 20; x++ {
		if buf.Get(x, 0).Char == 'C' {
			detailStart = x
			break
		}
	}
	if detailStart < 0 {
		t.Fatal("detail was not painted")
	}
	if detailStart-labelEnd < TableMinGap {
		t.Fatalf("gap=%d, want at least %d", detailStart-labelEnd, TableMinGap)
	}
}

func TestMenuInkKeepsMetaReadableOnFocus(t *testing.T) {
	ts := NewTokenSet(theme.DefaultDark())
	fg, _ := ts.ListRowColors(StateFocused)
	primary, muted := ts.MenuInk(StateFocused, fg)
	if !primary.Equal(fg) || !muted.Equal(fg) {
		t.Fatalf("focused meta must match row ink, primary=%v muted=%v fg=%v", primary, muted, fg)
	}
	idle, idleMuted := ts.MenuInk(StateDefault, ts.Color(ColorFg))
	if !idle.Equal(ts.Color(ColorFg)) {
		t.Fatal("idle primary must be ColorFg")
	}
	if !idleMuted.Equal(ts.Color(ColorMuted)) {
		t.Fatal("idle meta must be ColorMuted")
	}
}
