package ui

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// TableMinGap is the minimum space between a label and the next item, and
// between a left cluster and a right-aligned trailer. Menus, lists, and
// compact docks share this so items never sit flush against each other.
const TableMinGap = 2

// TableSep is the compact-table column separator. It is never used as filler.
const TableSep = " · "

type compactTablePlan struct {
	labelW int
	gapL   int
	colW   []int
	flex   int
}

// planCompactTable spends leftover row width as breathing room: half after
// the label, half before the last (usually inherited/meta) column. Column
// widths stay content-sized so values line up; extra space is never filler
// punctuation.
func planCompactTable(totalW, labelW, chevronW int, colMins []int) compactTablePlan {
	if labelW < 1 {
		labelW = 1
	}
	colW := append([]int(nil), colMins...)
	n := len(colW)
	sepW := rendering.VisibleWidth(TableSep)
	seps := 0
	if n > 1 {
		seps = n - 1
	}
	used := labelW + sumInts(colW) + sepW*seps + max(0, chevronW)
	extra := totalW - used
	gapL := TableMinGap
	flex := 0
	switch {
	case extra > TableMinGap:
		leftover := extra - TableMinGap
		gapL += leftover / 2
		flex = leftover - leftover/2
	case extra > 0:
		gapL = extra
	default:
		gapL = 1
	}
	return compactTablePlan{labelW: labelW, gapL: gapL, colW: colW, flex: flex}
}

// TrailingGap is leftover columns between a left-hand cluster and a
// right-aligned trailer. The gap is never tighter than TableMinGap.
func TrailingGap(total, used, trailing int) int {
	gap := total - used - trailing
	if gap < TableMinGap {
		return TableMinGap
	}
	return gap
}

func paintMenuDetail(buf *surface.CellBuffer, x, y, width int, label, detail string, fg, muted, bg cell.Color, attr cell.Attrs) {
	if width < 1 {
		return
	}
	if detail == "" {
		buf.WriteString(x, y, rendering.TruncateVisual(label, width, "…"), fg, bg, attr)
		return
	}
	detailW := rendering.VisibleWidth(detail)
	labelMax := width - TableMinGap - 1
	if labelMax < 1 {
		labelMax = 1
	}
	label = rendering.TruncateVisual(label, labelMax, "…")
	buf.WriteString(x, y, label, fg, bg, attr)
	used := rendering.VisibleWidth(label)
	remain := width - used
	if remain < TableMinGap+1 {
		return
	}
	gap := TrailingGap(width, used, detailW)
	if gap+detailW > remain {
		detail = rendering.TruncateVisual(detail, remain-TableMinGap, "…")
		gap = TableMinGap
	}
	buf.WriteString(x+used+gap, y, detail, muted, bg, 0)
}
