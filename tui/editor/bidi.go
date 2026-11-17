package editor

import (
	"crude/tui/bidi"
	"crude/tui/ui"
)

// Bidirectional text in the editor (see docs/reference/bidi.md).
//
// The buffer, carets, selections and every column the editor stores stay in
// logical order. Each source line is one paragraph: left-to-right by default,
// as code editors lay out code, or with the direction of its own first strong
// character when the user chose "By line". Render paints a row in logical
// order, decorations and carets included, and then arranges it, so every mark
// moves with its character. Pointer columns and the Up/Down goal column pass
// through the same row arrangement.

// editorLineBidi caches the resolved paragraph of one source line.
type editorLineBidi struct {
	valid      bool
	inline     *editorInlineLine
	generation uint64
	dir        bidi.Direction
	fallback   bool
	layout     *bidi.Layout
	// strong is the direction of the line's first strong character:
	// 0 unknown, 1 none, 2 left-to-right, 3 right-to-left.
	strong uint8
	// reorders records whether the line holds a character that can change
	// its visual order: 0 unknown, 1 no, 2 yes. Lines without one never
	// build a layout, so left-to-right code costs one scan per line text.
	reorders uint8
}

// TextDirection reports the editor's paragraph direction policy.
func (e *Editor) TextDirection() bidi.Direction {
	if e.directionByLine {
		return bidi.Auto
	}
	return bidi.LeftToRight
}

// SetTextDirection selects left-to-right lines (bidi.LeftToRight, the code
// editor default) or lines that take their own direction (bidi.Auto).
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
	fallback := dir == bidi.Auto && e.inheritsRightToLeft(lineIdx)
	// Inlay hints and ghost text are inline objects; they never reorder a
	// line on their own.
	if c.reorders == 1 && !fallback {
		return nil
	}
	inline := e.inlineLine(lineIdx)
	generation := bidi.Generation()
	if c.valid && c.inline == inline && c.generation == generation && c.dir == dir && c.fallback == fallback {
		return c.layout
	}
	clusters, _, widths, _ := e.lineRenderInfo(lineIdx, 0)
	units := make([]bidi.Unit, 0, len(clusters))
	for i := 0; i < len(clusters); {
		if inline != nil && i < len(inline.Runs) && (inline.Runs[i].HintIndex >= 0 || inline.Runs[i].Ghost) {
			// An inlay hint or ghost text is not source text: it moves as
			// one block and keeps its own reading order.
			first, text, width := inline.Runs[i], "", 0
			for i < len(clusters) && i < len(inline.Runs) && inline.Runs[i].HintIndex == first.HintIndex && inline.Runs[i].Ghost == first.Ghost {
				text += clusters[i]
				width += widths[i]
				i++
			}
			units = append(units, bidi.Unit{Text: text, Width: width, Object: true})
			continue
		}
		units = append(units, bidi.Unit{Text: clusters[i], Width: widths[i]})
		i++
	}
	*c = editorLineBidi{
		valid: true, inline: inline, generation: generation, dir: dir, fallback: fallback,
		layout: bidi.Analyze(units, dir, fallback), strong: c.strong, reorders: c.reorders,
	}
	return c.layout
}

// inheritsRightToLeft reports whether a line without strong characters
// keeps a right-to-left direction from the nearest line above it that has
// one (UAX #9 HL1). The look-back is bounded; past it the default applies.
func (e *Editor) inheritsRightToLeft(lineIdx int) bool {
	const lookBack = 64
	if e.lineStrong(lineIdx) != 1 {
		return false
	}
	for line := lineIdx - 1; line >= 0 && line >= lineIdx-lookBack; line-- {
		switch e.lineStrong(line) {
		case 2:
			return false
		case 3:
			return true
		}
	}
	return false
}

func (e *Editor) lineStrong(lineIdx int) uint8 {
	e.lineGeomInfo(lineIdx)
	cached := e.lineGeomCache[lineIdx]
	if cached == nil {
		return 1
	}
	if cached.bidi.strong == 0 {
		cached.bidi.strong = 1
		if rtl, found := bidi.StrongDirection(cached.raw); found {
			cached.bidi.strong = 2
			if rtl {
				cached.bidi.strong = 3
			}
		}
	}
	return cached.bidi.strong
}

// textColumns is the width of the text area between gutter and scrollbar.
func (e *Editor) textColumns() int {
	return max(1, e.viewW-e.gutterWidth()-e.scrollbarColWidth())
}

// rowSpan is the display line width of a row. Wrapped rows keep the last
// column for the continuation mark, so a right-to-left paragraph aligns to
// the wrap width on every row.
func (e *Editor) rowSpan() int {
	if e.wrapsRows() {
		return max(1, e.textColumns()-1)
	}
	return e.textColumns()
}

// rowLine arranges one visible row: the window of its source line that the
// row shows, on a line as wide as the text area. A wrapped row and the
// horizontally scrolled window of an unwrapped line are both display lines
// of the same paragraph.
func (e *Editor) rowLine(r visibleRow) *bidi.Line {
	layout := e.lineLayout(r.line)
	if layout == nil {
		return nil
	}
	span := e.rowSpan()
	end := r.end
	if end > r.start+span || end < r.start {
		end = r.start + span
	}
	return layout.Line(r.start, end, span)
}

// screenColumn maps a row's logical column (relative to the row start) to
// the screen column that shows it.
func (e *Editor) screenColumn(r visibleRow, col int) int {
	return e.rowLine(r).Visual(col)
}

// stepOnScreen moves the primary caret one cell left or right on its row.
// It reports false when the row paints as written, the caret is not on the
// row, or the caret is at the row's edge; the caller then moves in text
// order.
func (e *Editor) stepOnScreen(screenRight bool) bool {
	if !bidi.VisualCaret() {
		return false
	}
	rowIdx, col := e.visualPosition(e.cursor, e.wrapBefore)
	r := e.rowAt(rowIdx)
	ln := e.rowLine(r)
	if ln == nil {
		return false
	}
	if !e.wrapsRows() {
		col -= r.start
	}
	if col < 0 || col > ln.TextWidth() {
		return false
	}
	next, ok := ln.Step(col, screenRight)
	if !ok {
		return false
	}
	e.cursor = e.buf.snapPosition(Position{Line: r.line, Col: e.sourceColumn(r.line, r.start+next)})
	e.wrapBefore = false
	e.clearGoal()
	e.ensureCursorVisible()
	return true
}

// arrowForward resolves an arrow key to a text-order direction: on a
// right-to-left caret row the arrows point the other way.
func (e *Editor) arrowForward(screenRight bool) bool {
	if !bidi.VisualCaret() {
		return screenRight
	}
	rowIdx, _ := e.visualPosition(e.cursor, e.wrapBefore)
	return bidi.ArrowForward(screenRight, e.rowLine(e.rowAt(rowIdx)))
}

type caretCell struct{ x, y int }

// caretCells lists the block carets of the visible rows at their logical
// cells. Render paints them on each row before arranging that row.
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
		if !e.wrapsRows() {
			col -= e.scrollX
		}
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
