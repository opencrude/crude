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
	Grid       terminalvt.Projection
	Generation uint64
	Offset     int
	rows       map[int][]terminalvt.Cell
	wrapped    map[int]bool
	// Scroll intent and presented rows are separate. An asynchronous page read
	// cannot move the viewport into absent rows, or turn a pending End into an
	// ordinary numeric offset while the child keeps producing output.
	target          int
	follow          bool
	intent          uint64
	presented       [][]terminalvt.Cell
	presentedCursor terminalvt.Cursor
	presentedBottom int
	anchor, end     Point
	selected        bool
	selecting       bool
	clicks          int
	lastPress       uint64
	lastPoint       Point
}

// Install detaches a page and commits a complete requested viewport atomically.
// Only rows already outside the previous active grid are immutable across ingress
// generations. A former active row must be read again before copy or publication.
func (v *View) Install(g terminalvt.Projection, generation uint64) bool {
	if v.rows != nil && (generation < v.Generation || g.ReadSequence < v.Grid.ReadSequence) {
		return false
	}
	initial := v.rows == nil
	reset := v.rows == nil || g.HistoryEpoch != v.Grid.HistoryEpoch || g.Cols != v.Grid.Cols || g.GridRows != v.Grid.GridRows || g.Modes.AltScreen != v.Grid.Modes.AltScreen
	if reset {
		v.ClearSelection()
	} else if g.HistoryBase > v.Grid.HistoryBase {
		shift := int(g.HistoryBase - v.Grid.HistoryBase)
		// Cache keys are epoch-absolute. Pruning removes only the evicted rows,
		// never remaps an entire selected history on each new output line.
		for key := int(v.Grid.HistoryBase); key < int(g.HistoryBase); key++ {
			delete(v.rows, key)
			delete(v.wrapped, key)
		}
		v.target, v.Offset = max(0, v.target-shift), max(0, v.Offset-shift)
		if v.selected && (v.anchor.Y < shift || v.end.Y < shift) {
			v.ClearSelection()
		} else {
			v.anchor.Y, v.end.Y = v.anchor.Y-shift, v.end.Y-shift
		}
	}
	// Sync updates advance ingress generations without publishing new visual
	// rows. Keep detached same-history pages, never mix mutable rows into them.
	if reset {
		v.rows = make(map[int][]terminalvt.Cell)
		v.wrapped = make(map[int]bool)
		v.presented = nil
	} else if !g.Synchronized && (generation != v.Generation || v.Grid.Synchronized) {
		for key := int(v.Grid.HistoryBase) + v.Bottom(); key < int(v.Grid.HistoryBase)+v.Grid.Total; key++ {
			delete(v.rows, key)
			delete(v.wrapped, key)
		}
	}
	v.Generation = generation
	clone := g
	clone.Cells = make([][]terminalvt.Cell, len(g.Cells))
	clone.Wrapped = append([]bool(nil), g.Wrapped...)
	for i, row := range g.Cells {
		clone.Cells[i] = append([]terminalvt.Cell(nil), row...)
		v.rows[int(g.HistoryBase)+g.Start+i] = clone.Cells[i]
		v.wrapped[int(g.HistoryBase)+g.Start+i] = i < len(clone.Wrapped) && clone.Wrapped[i]
	}
	v.Grid = clone
	if initial {
		v.follow, v.intent = true, 1
	}
	if g.Modes.AltScreen {
		v.follow = true
	}
	v.target = min(v.target, v.Bottom())
	v.commitViewport()
	v.trimCache()
	return true
}
func (v *View) Bottom() int    { return max(0, v.Grid.Total-v.Grid.GridRows) }
func (v *View) Pinned() bool   { return v.follow }
func (v *View) Intent() uint64 { return v.intent }
func (v *View) DesiredOffset() int {
	if v.follow {
		return v.Bottom()
	}
	return v.target
}

// ViewportStart uses the service's active-grid sentinel for a follow intent, so
// output between admission and execution cannot leave End behind the live tail.
func (v *View) ViewportStart() int {
	if v.follow {
		return -1
	}
	return v.target
}
func (v *View) ScrollDelta(delta int) {
	if !v.Grid.Modes.AltScreen {
		v.ScrollTo(v.DesiredOffset() + delta)
	}
}
func (v *View) ScrollTop() {
	if !v.Grid.Modes.AltScreen {
		v.ScrollTo(0)
	}
}

func (v *View) ScrollTo(offset int) {
	offset = max(0, min(v.Bottom(), offset))
	// Historical rows not detached before DEC 2026 must wait for commit. Keep
	// the last usable viewport instead of replacing it with absent cells.
	if v.Grid.Synchronized {
		if _, _, missing := v.Missing(offset, offset+v.Grid.GridRows); missing {
			return
		}
	}
	follow := offset == v.Bottom()
	if offset != v.DesiredOffset() || follow != v.follow {
		v.intent++
	}
	v.target, v.follow = offset, follow
	v.commitViewport()
}
func (v *View) ScrollBottom()              { v.ScrollTo(v.Bottom()) }
func (v *View) Scrollbar() (int, int, int) { return v.Offset, v.Grid.Total, v.Grid.GridRows }
func (v *View) Row(y int) ([]terminalvt.Cell, bool) {
	r, ok := v.rows[int(v.Grid.HistoryBase)+y]
	return r, ok
}

// Missing returns one bounded page intent for a visible or selected interval.
func (v *View) Missing(start, end int) (int, int, bool) {
	start = max(0, start)
	end = min(v.Grid.Total, end)
	for y := start; y < end; y++ {
		if _, ok := v.Row(y); !ok {
			return y, min(terminalvt.MaxSnapshotRows, end-y), true
		}
	}
	return 0, 0, false
}
func (v *View) VisibleMissing() (int, int, bool) {
	offset := v.DesiredOffset()
	return v.Missing(offset, offset+v.Grid.GridRows)
}

func (v *View) commitViewport() {
	offset := v.DesiredOffset()
	if _, _, missing := v.Missing(offset, offset+v.Grid.GridRows); missing {
		return
	}
	v.Offset = offset
	v.presentedCursor, v.presentedBottom = v.Grid.Cursor, v.Bottom()
	v.presented = make([][]terminalvt.Cell, min(v.Grid.GridRows, v.Grid.Total-offset))
	for i := range v.presented {
		v.presented[i], _ = v.Row(offset + i)
	}
}

// Ordinary navigation retains a bounded working set, not a second full-history
// mirror. An explicit selection may retain its bounded source interval for copy.
func (v *View) trimCache() {
	if v.selected || len(v.rows) <= terminalvt.MaxSnapshotRows+2*v.Grid.GridRows {
		return
	}
	start := int(v.Grid.HistoryBase) + v.DesiredOffset()
	end := start + v.Grid.GridRows
	for y := range v.rows {
		if y < start || y >= end {
			delete(v.rows, y)
			delete(v.wrapped, y)
		}
	}
}
func (v *View) point(x, y int) Point {
	x = max(0, min(max(0, v.Grid.Cols-1), x))
	y = max(0, min(max(0, v.Grid.GridRows-1), y)) + v.Offset
	if row, ok := v.Row(y); ok {
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
	row, ok := v.Row(p.Y)
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
		row, _ := v.Row(y)
		for x, c := range row {
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
		if y < b.Y && v.wrapped[int(v.Grid.HistoryBase)+y] {
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
		if y >= len(v.presented) {
			break
		}
		row := v.presented[y]
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
			cursor := v.presentedCursor
			if v.Pinned() && v.Offset == v.presentedBottom && cursor.Visible && (!focused || blink) && x <= cursor.X && cursor.X < x+width && y == cursor.Y {
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
