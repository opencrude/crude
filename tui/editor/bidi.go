package editor

import (
	"strings"

	"crude/tui/bidi"
	"crude/tui/ui"
)

// Bidirectional text in the editor (see docs/reference/bidi.md).
//
// The outer row is a left-aligned code grid. Prose inside it can have a
// different reading direction. Scrolling never supplies a line boundary.
type editorLineBidi struct {
	valid      bool
	inline     *editorInlineLine
	generation uint64
	dir        bidi.Direction
	roles      *byte
	layout     *bidi.Layout
	reorders   uint8
	// For wrapped lines only the current wrap width owns these ranges.
	full      *bidi.Line
	wrapWidth int
	wrapped   map[[2]int]*bidi.Line
}

// TextDirection reports the editor's paragraph direction policy.
func (e *Editor) TextDirection() bidi.Direction {
	if e.directionByLine {
		return bidi.Auto
	}
	return bidi.LeftToRight
}

// SetTextDirection selects lines that take their own direction (bidi.Auto,
// the default) or left-to-right lines (bidi.LeftToRight).
func (e *Editor) SetTextDirection(dir bidi.Direction) {
	byLine := dir == bidi.Auto
	if e.directionByLine == byLine {
		return
	}
	e.directionByLine = byLine
	e.stopPointerSelection()
	e.CancelPointerCapture()
	e.clearGoal()
}

// lineLayout returns the resolved paragraph of a source line, or nil when it
// paints as written.
func (e *Editor) lineLayout(lineIdx int) *bidi.Layout {
	if lineIdx < 0 || lineIdx >= e.LineCount() || !bidi.Current().Arrange {
		return nil
	}
	e.lineGeomInfo(lineIdx)
	cached := e.lineGeomCache[lineIdx]
	if cached == nil {
		return nil
	}
	c := &cached.bidi
	if c.reorders == 0 {
		c.reorders = 1
		if bidi.NeedsLayout(cached.raw) {
			c.reorders = 2
		}
	}
	dir := e.TextDirection()
	// Inlay hints are inline objects; they never reorder a line on their
	// own. Suggested text previews source text, so right-to-left suggested
	// text lays out the line as the accepted text would.
	ghostRTL := false
	if g := e.ghost; g != nil && g.buffer == e.buf && g.atLine == lineIdx && !g.marker {
		ghostRTL = bidi.NeedsLayout(g.label)
	}
	if c.reorders == 1 && !ghostRTL {
		return nil
	}
	inline := e.inlineLine(lineIdx)
	generation := bidi.Generation()
	roles := e.lineRoles(lineIdx)
	var rolesID *byte
	if len(roles) > 0 {
		rolesID = &roles[0]
	}
	if c.valid && c.inline == inline && c.generation == generation && c.dir == dir && c.roles == rolesID {
		return c.layout
	}
	clusters, _, widths, _ := e.lineRenderInfo(lineIdx)
	units := make([]bidi.Unit, 0, len(clusters))
	// cols holds the source column of each unit; -1 for inlay hints and
	// suggested text, which have no syntax role.
	var tabs [][2]int
	if strings.ContainsRune(cached.raw, '\t') {
		tabs = cached.spaceGeometry(e.TabWidth(), true).tabs
	}
	var cols []int
	if rolesID != nil || len(tabs) > 0 {
		cols = make([]int, 0, len(clusters))
	}
	sourceCol := 0
	for i := 0; i < len(clusters); {
		if inline != nil && i < len(inline.Runs) && inline.Runs[i].HintIndex >= 0 && !inline.Runs[i].Ghost {
			// An inlay hint is not source text: it moves as one block and
			// keeps its own reading order. Suggested text is laid out per
			// cluster, as the text it inserts.
			first, text, width := inline.Runs[i], "", 0
			for i < len(clusters) && i < len(inline.Runs) && inline.Runs[i].HintIndex == first.HintIndex && !inline.Runs[i].Ghost {
				text += clusters[i]
				width += widths[i]
				i++
			}
			units = append(units, bidi.Unit{Text: text, Width: width, Object: true})
			if cols != nil {
				cols = append(cols, -1)
			}
			continue
		}
		units = append(units, bidi.Unit{Text: clusters[i], Width: widths[i]})
		if cols != nil {
			switch {
			case inline == nil:
				cols = append(cols, sourceCol)
				sourceCol += widths[i]
			case i < len(inline.Runs) && !inline.Runs[i].Ghost:
				cols = append(cols, inline.Runs[i].SourceCol)
			default:
				cols = append(cols, -1)
			}
		}
		i++
	}
	role := func(i int) (byte, bool) {
		if cols != nil {
			if col := cols[i]; col >= 0 && col < len(roles) {
				return roles[col] & roleMask, roles[col]&roleTokenStart != 0
			}
		}
		return roleNone, false
	}
	// IntelliJ resolves runs on either side of each tab independently.
	// An expanded tab occupies source grid cells, never part of an RTL run.
	start := 0
	isolateField := func(end int) {
		if end > start {
			isolateSource(units[start:end], func(i int) (byte, bool) { return role(start + i) }, dir == bidi.Auto)
		}
	}
	if len(tabs) > 0 {
		t := 0
		for i, col := range cols {
			for t < len(tabs) && col >= tabs[t][1] {
				t++
			}
			if t == len(tabs) {
				break
			}
			if col >= tabs[t][0] {
				isolateField(i)
				start = i + 1
			}
		}
	}
	isolateField(len(units))
	*c = editorLineBidi{valid: true, inline: inline, generation: generation, dir: dir, roles: rolesID, reorders: c.reorders, layout: bidi.Analyze(units, bidi.LeftToRight, false)}
	return c.layout
}

func (e *Editor) textColumns() int { return max(1, e.viewW-e.gutterWidth()-e.scrollbarColWidth()) }

// rowLine resolves a complete display row. Only a real soft wrap splits a
// logical line. The extra cell is the editor's block caret at end of text;
// viewport padding is not part of the bidi paragraph.
func (e *Editor) rowLine(r visibleRow) *bidi.Line {
	layout := e.lineLayout(r.line)
	if layout == nil {
		return nil
	}
	c := &e.lineGeomCache[r.line].bidi
	end := min(r.end, layout.Width())
	if r.start == 0 && end == layout.Width() {
		if c.full == nil {
			c.full = layout.Line(0, end, end+1)
		}
		return c.full
	}
	key := [2]int{r.start, end}
	if c.wrapWidth != e.rows.width || c.wrapped == nil {
		c.wrapped = make(map[[2]int]*bidi.Line)
		c.wrapWidth = e.rows.width
	}
	if ln := c.wrapped[key]; ln != nil {
		return ln
	}
	ln := layout.Line(r.start, end, end-r.start+1)
	c.wrapped[key] = ln
	return ln
}

// Walk the same row the user sees, including when the neighboring cell
// lies outside the viewport. Inlay cells and expanded tabs may map to the
// same source caret, so skip them rather than getting stuck there.
func (e *Editor) stepOnScreen(right bool) bool {
	if !bidi.VisualCaret() {
		return false
	}
	idx, col := e.logicalRowPosition(e.cursor, e.wrapBefore)
	r := e.rowAt(idx)
	ln := e.rowLine(r)
	if ln != nil {
		for {
			next, ok := ln.Step(col, right)
			if !ok {
				break
			}
			col = next
			pos := e.buf.snapPosition(Position{Line: r.line, Col: e.sourceColumn(r.line, r.start+col)})
			before := r.continues && col == ln.TextWidth()
			if pos != e.cursor || before != e.wrapBefore {
				e.setVisualCaret(pos, before)
				return true
			}
		}
	} else if right && col < min(e.displayColumn(r.line, e.lineVisualWidth(r.line))-r.start, r.end-r.start) || !right && col > 0 {
		return false
	}
	// The physical edge connects to the opposite edge of the neighboring
	// display row, not an arbitrary logical neighbor inside the current row.
	adjacent := idx - 1
	if right {
		adjacent = idx + 1
	}
	if adjacent < 0 || adjacent >= e.visualRowCount() {
		return ln != nil
	}
	target := e.rowAt(adjacent)
	if target.ghostRow != 0 {
		return false
	}
	other := e.rowLine(target)
	if ln == nil && other == nil {
		return false
	}
	x := 0
	if !right {
		x = min(target.end, e.displayColumn(target.line, e.lineVisualWidth(target.line))) - target.start
	}
	pos, before := e.positionOnRow(adjacent, x-e.scrollX)
	e.setVisualCaret(pos, before)
	return true
}
func (e *Editor) setVisualCaret(pos Position, before bool) {
	e.cursor, e.wrapBefore = pos, before
	e.clearGoal()
	e.ensureCursorVisible()
}

// Word motion uses the direction of the run at the caret, not the grid.
func (e *Editor) arrowForward(right bool) bool {
	if !bidi.VisualCaret() {
		return right
	}
	row, col := e.logicalRowPosition(e.cursor, e.wrapBefore)
	ln := e.rowLine(e.rowAt(row))
	if bs := ln.BlocksFrom(ln.Visual(col)); len(bs) > 0 && bs[0].Level%2 != 0 {
		return !right
	}
	return right
}

type caretCell struct{ x, y int }

// caretCells lists the block carets of the visible rows at their
// screen cells after direction and viewport transforms.
func (e *Editor) caretCells(ctx ui.PaintContext, sw, sbX, bodyH int) []caretCell {
	if !ctx.Focused || e.readOnly || !e.cursorBlink.Visible(ctx.Frame) {
		return nil
	}
	cells := e.caretScratch[:0]
	defer func() { e.caretScratch = cells[:0] }()
	e.prepareMultiplePaint()
	for _, r := range e.multiPaint.ranges {
		if _, hidden := e.hiddenFold(r.cursor.Line); hidden {
			continue
		}
		y, col := e.visualPosition(r.cursor, r.before)
		y -= e.scrollRow()
		col -= e.scrollX
		if x := sw + col; x >= sw && x < sbX && y >= 0 && y < bodyH {
			cells = append(cells, caretCell{x, y})
		}
	}
	cursorY, cursorCol := e.viewPosition(e.cursor)
	if x := sw + cursorCol; x >= sw && x < sbX && cursorY >= 0 && cursorY < bodyH {
		cells = append(cells, caretCell{x, cursorY})
	}
	return cells
}
