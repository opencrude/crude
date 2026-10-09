// Package termview owns client-local terminal presentation. It never parses VT
// bytes or generates child protocol replies.
package termview

import (
	"crude/terminalvt"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	"strings"
	"unicode"
)

type Point struct{ X, Y int }
type View struct {
	Grid        terminalvt.Projection
	Generation  uint64
	Offset      int
	rows        map[int][]terminalvt.Cell
	wrapped     map[int]bool
	anchor, end Point
	selected    bool
	selecting   bool
	clicks      int
	lastPress   uint64
	lastPoint   Point
}

// Install detaches the page before publishing it. Source changes invalidate
// cached cells, but selection coordinates survive while history identity holds.
func (v *View) Install(g terminalvt.Projection, generation uint64) bool {
	if v.rows != nil && generation < v.Generation {
		return false
	}
	pinned := v.rows == nil || v.Pinned()
	reset := v.rows == nil || g.HistoryEpoch != v.Grid.HistoryEpoch || g.Cols != v.Grid.Cols || g.GridRows != v.Grid.GridRows || g.Modes.AltScreen != v.Grid.Modes.AltScreen
	if reset {
		v.ClearSelection()
	}
	if reset || generation != v.Generation {
		v.rows = make(map[int][]terminalvt.Cell)
		v.wrapped = make(map[int]bool)
	}
	v.Generation = generation
	clone := g
	clone.Cells = make([][]terminalvt.Cell, len(g.Cells))
	clone.Wrapped = append([]bool(nil), g.Wrapped...)
	for i, row := range g.Cells {
		clone.Cells[i] = append([]terminalvt.Cell(nil), row...)
		v.rows[g.Start+i] = clone.Cells[i]
		v.wrapped[g.Start+i] = i < len(clone.Wrapped) && clone.Wrapped[i]
	}
	v.Grid = clone
	if pinned || g.Modes.AltScreen {
		v.Offset = v.Bottom()
	} else {
		v.Offset = min(v.Offset, v.Bottom())
	}
	return true
}
func (v *View) Bottom() int  { return max(0, v.Grid.Total-v.Grid.GridRows) }
func (v *View) Pinned() bool { return v.Offset == v.Bottom() }
func (v *View) ScrollDelta(delta int) {
	if !v.Grid.Modes.AltScreen {
		v.Offset = max(0, min(v.Bottom(), v.Offset+delta))
	}
}
func (v *View) ScrollTop() {
	if !v.Grid.Modes.AltScreen {
		v.Offset = 0
	}
}
func (v *View) ScrollBottom()                       { v.Offset = v.Bottom() }
func (v *View) Scrollbar() (int, int, int)          { return v.Offset, v.Grid.Total, v.Grid.GridRows }
func (v *View) Row(y int) ([]terminalvt.Cell, bool) { r, ok := v.rows[y]; return r, ok }

// Missing returns one bounded page intent for a visible or selected interval.
func (v *View) Missing(start, end int) (int, int, bool) {
	start = max(0, start)
	end = min(v.Grid.Total, end)
	for y := start; y < end; y++ {
		if _, ok := v.rows[y]; !ok {
			return y, min(terminalvt.MaxSnapshotRows, end-y), true
		}
	}
	return 0, 0, false
}
func (v *View) VisibleMissing() (int, int, bool) {
	return v.Missing(v.Offset, v.Offset+v.Grid.GridRows)
}
func (v *View) point(x, y int) Point {
	x = max(0, min(max(0, v.Grid.Cols-1), x))
	y = max(0, min(max(0, v.Grid.GridRows-1), y)) + v.Offset
	if row, ok := v.rows[y]; ok {
		for x > 0 && x < len(row) && row[x].Width == 0 {
			x--
		}
	}
	return Point{x, y}
}
func less(a, b Point) bool { return a.Y < b.Y || a.Y == b.Y && a.X < b.X }
func (v *View) Range() (Point, Point) {
	a, b := v.anchor, v.end
	if less(b, a) {
		a, b = b, a
	}
	return a, b
}
func (v *View) HasSelection() bool { return v.selected }
func (v *View) ClearSelection()    { v.selected, v.selecting = false, false }
func (v *View) SelectAll() {
	v.anchor = Point{0, 0}
	v.end = Point{max(0, v.Grid.Cols-1), max(0, v.Grid.Total-1)}
	v.selected = v.Grid.Total > 0
}
func word(c terminalvt.Cell) bool {
	for _, r := range c.Text {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
	}
	return false
}
func (v *View) SelectionPress(x, y int, ns uint64) {
	p := v.point(x, y)
	if ns >= v.lastPress && ns-v.lastPress < 500_000_000 && p == v.lastPoint {
		v.clicks = v.clicks%3 + 1
	} else {
		v.clicks = 1
	}
	v.lastPress, v.lastPoint = ns, p
	v.anchor, v.end = p, p
	v.selected = false
	v.selecting = true
	row, ok := v.rows[p.Y]
	if !ok {
		return
	}
	if v.clicks == 3 {
		v.anchor.X = 0
		v.end.X = max(0, v.Grid.Cols-1)
		v.selected = true
	}
	if v.clicks == 2 && p.X < len(row) {
		kind := word(row[p.X])
		a, b := p.X, p.X
		for a > 0 && (row[a-1].Width == 0 || word(row[a-1]) == kind) {
			a--
		}
		for b+1 < len(row) && (row[b+1].Width == 0 || word(row[b+1]) == kind) {
			b++
		}
		v.anchor.X, v.end.X = a, b
		v.selected = true
	}
}
func (v *View) SelectionDrag(x, y int) {
	if !v.selecting {
		return
	}
	if y < 0 {
		v.ScrollDelta(-1)
	} else if y >= v.Grid.GridRows {
		v.ScrollDelta(1)
	}
	p := v.point(x, y)
	v.end = p
	v.selected = true
	if v.clicks == 3 {
		if less(p, v.anchor) {
			v.end.X = 0
		} else {
			v.end.X = max(0, v.Grid.Cols-1)
		}
	}
}
func (v *View) SelectionRelease(_, _ int) bool {
	v.selecting = false
	return v.selected
}
func (v *View) SelectionMissing() (int, int, bool) { a, b := v.Range(); return v.Missing(a.Y, b.Y+1) }

// SelectionText fails closed while any selected row is absent. Soft wraps join
// physical rows without trimming source spaces; hard line endings trim padding.
func (v *View) SelectionText() (string, bool) {
	if !v.selected {
		return "", true
	}
	if _, _, missing := v.SelectionMissing(); missing {
		return "", false
	}
	a, b := v.Range()
	var text strings.Builder
	for y := a.Y; y <= b.Y; y++ {
		var line strings.Builder
		for x, c := range v.rows[y] {
			if c.Width == 0 {
				continue
			}
			if y == a.Y && x+max(1, int(c.Width))-1 < a.X || y == b.Y && x > b.X {
				continue
			}
			if c.Text == "" {
				line.WriteByte(' ')
			} else {
				line.WriteString(c.Text)
			}
		}
		if y < b.Y && v.wrapped[y] {
			text.WriteString(line.String())
		} else {
			text.WriteString(strings.TrimRight(line.String(), " \t"))
			if y < b.Y {
				text.WriteByte('\n')
			}
		}
	}
	return text.String(), true
}
func color(c terminalvt.Color, fallback cell.Color) cell.Color {
	return rendering.TerminalColor(c, fallback)
}
func attrs(s terminalvt.Style) cell.Attrs {
	var a cell.Attrs
	if s.Bold {
		a |= cell.AttrBold
	}
	if s.Faint {
		a |= cell.AttrDim
	}
	if s.Italic {
		a |= cell.AttrItalic
	}
	if s.Underline {
		a |= cell.AttrUnderline
	}
	if s.Strikethrough {
		a |= cell.AttrStrikethrough
	}
	if s.Inverse {
		a |= cell.AttrReverse
	}
	return a
}
func (v *View) Paint(buf *surface.CellBuffer, ox, oy, w, h int, t *ui.TokenSet, focused, blink bool) {
	fg, bg := t.Color(ui.ColorFg), t.Color(ui.ColorBg)
	a, b := v.Range()
	for y := 0; y < min(h, v.Grid.GridRows); y++ {
		row := v.rows[v.Offset+y]
		for x, c := range row {
			if x >= w {
				break
			}
			if c.Width == 0 {
				continue
			}
			width := max(1, int(c.Width))
			if x+width > w {
				continue
			}
			text := c.Text
			if text == "" {
				text = " "
			}
			out := cell.NewClusterCell(text, width, color(c.FG, fg), color(c.BG, bg), attrs(c.Style))
			p := Point{x, v.Offset + y}
			end := Point{x + width - 1, p.Y}
			if v.selected && !less(end, a) && !less(b, p) {
				out.FG = t.Color(ui.ColorTextSelectionFg)
				out.BG = t.Color(ui.ColorTextSelection)
				out.Attr &^= cell.AttrReverse | cell.AttrDim
			}
			cursor := v.Grid.Cursor
			if v.Pinned() && cursor.Visible && (!focused || blink) && x <= cursor.X && cursor.X < x+width && y == cursor.Y {
				out.FG = t.Color(ui.ColorCursor)
				out.Attr ^= cell.AttrReverse
				if !focused {
					out.Attr |= cell.AttrDim
				}
			}
			buf.Set(ox+x, oy+y, out)
		}
	}
}
