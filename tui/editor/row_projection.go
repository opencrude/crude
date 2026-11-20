package editor

import (
	"math"
	"slices"
)

type cachedRowMetrics struct {
	source  rowMetrics
	inline  *editorInlineLine
	current bool
}

// editorRows is view-local geometry, never a copy of document state. The source
// change journal relocates retained metrics; only changed lines are measured.
// Width changes reuse those metrics, and steady paint/scroll do no source scan.
type editorRows struct {
	sourceRows
	buffer          *PieceTable
	revision        uint64
	tabWidth, width int
	metrics         []cachedRowMetrics
	inlays          *editorInlayProjection
	ghost           *editorGhost
	ghostText       string
	ghostLine       int
	ghostRows       int
	review          *reviewLayout
	wrapped         bool
	foldGeneration  uint64
	measures        uint64
	atomicArena     []rowSpan
	breakArena      []int
}

func (p *editorRows) prepare(e *Editor, width int, review *reviewLayout) (ready bool) {
	e.remapFolds()
	if !e.wordWrap {
		p.prepareUnwrapped(e, review)
		return true
	}

	if p.needsCold(e) {
		return false
	}
	defer func() {
		if !ready {
			p.buffer = nil
		}
	}()
	width = max(1, width)
	count := e.LineCount()
	dirtyFrom, dirtyTo := count, 0
	if p.revision != e.Revision() {
		for next := p.revision + 1; next <= e.Revision(); next++ {
			change := e.buf.lineChanges[next%uint64(len(e.buf.lineChanges))]
			if change.revision != next || change.start < 0 || change.end >= len(p.metrics) {
				return false
			}
			a, b := change.start, change.end+1
			replacement := b - a + change.delta
			if replacement <= 0 {
				return false
			}
			if change.delta == 0 {
				clear(p.metrics[a:b])
			} else {
				// Card placements are keyed by source line. Remove only
				// those overlays before the journal relocates source rows.
				p.setReviewRows(nil)
				p.metrics = slices.Replace(p.metrics, a, b, make([]cachedRowMetrics, replacement)...)
				p.lines = slices.Replace(p.lines, a, b, make([]rowLine, replacement)...)
				p.prefix = make([]int, len(p.lines)+1)
				p.valid = 0
				dirtyTo = count // earlier dirty lines may have shifted to the tail
			}
			dirtyFrom = min(dirtyFrom, a)
			dirtyTo = max(dirtyTo, b+change.delta)
		}
	}
	if len(p.metrics) != count {
		return false
	}
	p.buffer, p.revision, p.tabWidth = e.buf, e.Revision(), e.TabWidth()
	reflow := p.width != width || p.foldGeneration != e.folds.hiddenGeneration
	p.foldGeneration = e.folds.hiddenGeneration
	p.width = width
	inlays, ghost := e.currentInlays(), e.currentGhost()
	// Label/ghost changes have independent source-qualified identities. The old
	// and new line sets are small; untouched offscreen lines keep their metrics.
	inlineDirty := func(line int) {
		if line >= 0 && line < count {
			p.metrics[line].current = false
			dirtyFrom = min(dirtyFrom, line)
			dirtyTo = max(dirtyTo, line+1)
		}
	}
	if inlays != p.inlays {
		if p.inlays != nil {
			for line := range p.inlays.lines {
				inlineDirty(line)
			}
		}
		if inlays != nil {
			for line := range inlays.lines {
				inlineDirty(line)
			}
		}
	}
	text := ""
	line := -1
	if ghost != nil {
		text, line = ghost.text, ghost.atLine
	}
	if ghost != p.ghost || text != p.ghostText || line != p.ghostLine {
		inlineDirty(p.ghostLine)
		inlineDirty(line)
	}
	p.inlays, p.ghost, p.ghostText, p.ghostLine = inlays, ghost, text, line
	if reflow {
		dirtyFrom, dirtyTo = 0, count
	}
	for line := dirtyFrom; line < min(count, dirtyTo); line++ {
		m := &p.metrics[line]
		if m.current && !reflow {
			continue
		}
		if !m.current {
			raw, _ := e.buf.lineView(line)
			m.source = measureRowMetrics(string(raw), e.TabWidth())
			m.inline = e.inlineLine(line)
			if m.inline != nil {
				// Projected runs already carry exact grapheme and tab-expanded widths.
				// Measure their literal display sequence without including marker paint.
				var projected rowMetrics
				seenText := false
				for i, run := range m.inline.Runs {
					if run.Width > 1 {
						projected.atomic = append(projected.atomic, rowSpan{projected.width, projected.width + run.Width})
					}
					projected.width += run.Width
					if run.Cluster == " " {
						if seenText && (i+1 == len(m.inline.Runs) || m.inline.Runs[i+1].Cluster != " ") {
							projected.breaks = append(projected.breaks, projected.width)
						}
					} else {
						seenText = true
					}
				}
				m.source = projected
			}
			m.current = true
			p.measures++
		}
		layout := wrapRowMetrics(m.source, width, p.lines[line].runs)
		if _, hidden := e.hiddenFold(line); hidden {
			layout.count = 0
		} else {
			if extra := e.ghostRows(line); extra > 0 {
				layout.ghost = int32(extra)
				layout.count += extra
			}
			if p.review != nil {
				addReviewRowCount(&layout, len(p.review.lineRows(line)))
			}
		}
		p.set(line, layout)
	}
	p.setReviewRows(review)
	return true
}

func addReviewRowCount(layout *rowLine, count int) {
	layout.count += count - int(layout.review)
	layout.review = int32(count)
}

func (p *editorRows) setReviewRows(next *reviewLayout) {
	if next == p.review {
		return
	}
	update := func(line, count int) {
		layout := p.lines[line]
		if layout.count > 0 && int(layout.review) != count {
			addReviewRowCount(&layout, count)
			p.set(line, layout)
		}
	}
	if p.review != nil {
		for line := range p.review.lines {
			update(line, len(next.lineRows(line)))
		}
	}
	if next != nil {
		for line, rows := range next.lines {
			update(line, len(rows))
		}
	}
	p.review = next
}

// scrollY remains the top source line. wrapOffset is its local visual row, so
// peer edits and retained navigation keep using the existing source-line anchor.
func (e *Editor) usesRows() bool {
	if !e.open || e.rowsStopped || e.viewW < 10 || e.viewH < 4 {
		return false
	}
	var review *reviewLayout
	if len(e.review.comments) != 0 {
		review = e.currentReviewRows()
	}
	if !e.wordWrap && len(e.folds.hidden) == 0 && !e.hasGhostRows() && review == nil {
		if e.rows.review != nil {
			e.rows.setReviewRows(nil)
			e.wrapOffset = 0
		}
		return false
	}
	if !e.rows.prepare(e, max(1, e.viewW-e.gutterWidth()-e.scrollbarColWidth()-1), review) {
		e.startRows()
		// Cold wrapping never reveals a deliberately collapsed source range.
		// The same index keeps its fold-only identity until exact metrics arrive.
		if len(e.folds.hidden) > 0 || e.hasGhostRows() || review != nil {
			e.rows.prepareUnwrapped(e, review)
			return true
		}
		return false
	}
	return true
}
func (e *Editor) prepareRows() *editorRows {
	return &e.rows
}
func (e *Editor) visualRowCount() int {
	if !e.usesRows() {
		return e.LineCount()
	}
	return e.prepareRows().total()
}
func (e *Editor) scrollRow() int {
	if !e.usesRows() {
		return e.scrollY
	}
	p := e.prepareRows()
	line := min(max(0, e.scrollY), max(0, len(p.lines)-1))
	return p.before(line) + min(max(0, e.wrapOffset), max(0, p.lines[line].count-1))
}
func (e *Editor) setScrollRow(row int) {
	row = max(0, row)
	if !e.usesRows() {
		e.scrollY = row
		e.wrapOffset = 0
		return
	}
	e.scrollY, e.wrapOffset = e.prepareRows().at(row)
}

type visibleRow struct {
	line, start, end, index int
	continues               bool
	// Display-only row indexes are one-based; both are 0 for source text.
	ghostRow  int
	reviewRow int
}

func (e *Editor) rowAt(row int) visibleRow {
	if !e.usesRows() {
		return visibleRow{line: row, end: math.MaxInt}
	}
	p := e.prepareRows()
	if row >= p.total() {
		return visibleRow{line: e.LineCount()}
	}
	line, local := p.at(row)
	r := p.lines[line]
	if extra := local - r.textRows(); extra >= 0 {
		if extra < int(r.ghost) {
			return visibleRow{line: line, end: math.MaxInt, index: local, ghostRow: extra + 1}
		}
		return visibleRow{line: line, end: math.MaxInt, index: local, reviewRow: extra - int(r.ghost) + 1}
	}
	if !e.wordWrap || !p.wrapped {
		return visibleRow{line: line, end: math.MaxInt}
	}
	return visibleRow{line: line, start: r.start(local), end: r.end(local), index: local, continues: local+1 < r.textRows()}
}
func (e *Editor) logicalRowPosition(pos Position, before bool) (row, col int) {
	col = e.displayColumn(pos.Line, pos.Col)
	if !e.usesRows() {
		return pos.Line, col
	}
	p := e.prepareRows()
	line := min(max(0, pos.Line), max(0, len(p.lines)-1))
	layout := p.lines[line]
	if !e.wordWrap || !p.wrapped {
		return p.before(line), col
	}
	local := layout.atColumn(col)
	if before && local > 0 && layout.start(local) == col {
		local--
	}
	return p.before(line) + local, col - layout.start(local)
}

// visualPosition uses document-space visual columns, independent of scroll.
func (e *Editor) visualPosition(pos Position, before bool) (row, col int) {
	row, col = e.logicalRowPosition(pos, before)
	return row, e.rowLine(e.rowAt(row)).Visual(col)
}
func (e *Editor) viewPosition(pos Position) (row, col int) {
	row, col = e.visualPosition(pos, pos == e.cursor && e.wrapBefore)
	return row - e.scrollRow(), col - e.scrollX
}

// rowDisplayColumn inverts that transform for pointer and inlay hit testing.
func (e *Editor) rowDisplayColumn(r visibleRow, screen int) int {
	return min(r.end, r.start+e.rowLine(r).Logical(max(0, screen+e.scrollX)))
}
func (e *Editor) positionOnRow(row, column int) (Position, bool) {
	row = min(max(0, row), max(0, e.visualRowCount()-1))
	r := e.rowAt(row)
	display := e.rowDisplayColumn(r, column)
	pos := e.buf.snapPosition(Position{Line: r.line, Col: e.sourceColumn(r.line, display)})
	return pos, r.continues && e.displayColumn(pos.Line, pos.Col) == r.end
}
func (e *Editor) moveVisualRows(delta int) {
	row, _ := e.visualPosition(e.cursor, e.wrapBefore)
	target := row + delta
	if e.rows.review != nil {
		last := e.visualRowCount() - 1
		target = e.reviewCaretRow(min(last, max(0, target)), delta)
		if target > last {
			target = e.reviewCaretRow(last, -1)
		}
		if target == row {
			return
		}
	}
	e.setGoal()
	e.cursor, e.wrapBefore = e.positionOnRow(target, e.goalCol-e.scrollX)
	e.ensureCursorVisible()
}

// reviewCaretRow skips display-only review rows without searching source text.
func (e *Editor) reviewCaretRow(row, direction int) int {
	if e.rows.review == nil || row < 0 {
		return row
	}
	r := e.rowAt(row)
	if r.reviewRow == 0 {
		return row
	}
	p := e.prepareRows()
	if direction < 0 {
		return p.before(r.line) + p.lines[r.line].textRows() - 1
	}
	return p.before(r.line + 1)
}

// setGoal records the column vertical movement keeps: the caret's screen
// column on its arranged row, so Up and Down land on the character shown
// in that column, also inside a right-to-left run. On unwrapped lines the
// goal counts from the line start, so horizontal scrolling between moves
// does not shift it.
func (e *Editor) setGoal() {
	if e.goalSet {
		return
	}
	_, e.goalCol = e.visualPosition(e.cursor, e.wrapBefore)
	e.goalSet = true
}

func (e *Editor) setWrapping(on bool) {
	if e.wordWrap == on {
		return
	}
	visible := e.inlayCaretVisible()
	e.stopPointerSelection()
	e.CancelPointerCapture()
	e.wordWrap = on
	e.resetSecondaryGoals()
	if !on {
		e.cancelRows()
	}
	e.wrapOffset = 0
	e.wrapBefore = false
	e.clearGoal()
	if on {
		e.scrollX = 0
	}
	e.pendingReveal = e.pendingReveal || visible
}

func (p *editorRows) needsCold(e *Editor) bool {
	if !p.wrapped || p.buffer != e.buf || p.tabWidth != e.TabWidth() || p.revision > e.Revision() || e.Revision()-p.revision > uint64(len(e.buf.lineChanges)) {
		return true
	}
	changedBytes := 0
	for next := p.revision + 1; next <= e.Revision(); next++ {
		c := e.buf.lineChanges[next%uint64(len(e.buf.lineChanges))]
		if c.revision != next {
			return true
		}
		// The measured cold pass takes ~27 ms for two megabytes. Keep at
		// most 64 KiB of changed-line measurement on an input turn; larger
		// lines/pastes use the same cancellable source worker as first open.
		for line := max(0, c.start); line <= min(e.LineCount()-1, c.end+max(0, c.delta)); line++ {
			raw, _ := e.buf.lineView(line)
			changedBytes += len(raw)
			if changedBytes > 64<<10 {
				return true
			}
		}
	}
	return false
}

func (e *Editor) wrapsRows() bool { return e.wordWrap && e.usesRows() && e.rows.wrapped }
func (p *editorRows) prepareUnwrapped(e *Editor, review *reviewLayout) {
	count := e.LineCount()
	ghost := e.currentGhost()
	ghostLine, ghostRows := -1, 0
	if ghost != nil {
		ghostLine, ghostRows = ghost.atLine, e.ghostRows(ghost.atLine)
	}
	if p.buffer == e.buf && len(p.lines) == count && !p.wrapped && p.foldGeneration == e.folds.hiddenGeneration &&
		p.ghostLine == ghostLine && p.ghostRows == ghostRows {
		p.revision = e.Revision()
		p.setReviewRows(review)
		return
	}
	p.reset(count)
	p.review = nil
	for line := 0; line < count; line++ {
		rows := 1
		layout := rowLine{}
		if _, hidden := e.hiddenFold(line); hidden {
			rows = 0
		} else if line == ghostLine && ghostRows > 0 {
			layout.ghost = int32(ghostRows)
			rows += ghostRows
		}
		layout.count = rows
		p.set(line, layout)
	}
	p.buffer, p.revision, p.wrapped, p.foldGeneration = e.buf, e.Revision(), false, e.folds.hiddenGeneration
	p.ghostLine, p.ghostRows = ghostLine, ghostRows
	p.setReviewRows(review)
}
