package gitdiff

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/diffview"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

// Text sides of a line row: one stream in unified, two columns in split.
const (
	sideUnified = iota
	sideOld
	sideNew
)

type point struct{ row, column int }

// selection is a character range over line rows on one side. Dragging copies
// on release and drops the highlight, like every other text surface.
type selection struct {
	anchor, end point
	side        int
	active      bool
	dragging    bool
}

// ---- hunks ----------------------------------------------------------------

func (document *Document) hunkAllowed(fileIndex int, op Op) bool {
	file := &document.files[fileIndex]
	if document.readOnly || file.Historical || file.Comparison.Fingerprint == "" || file.Comparison.OriginalPath != "" || file.Comparison.Binary {
		return false
	}
	switch op {
	case OpStageHunk:
		return file.Change.Area == workspace.GitAreaWorking || file.Change.Area == workspace.GitAreaUntracked
	case OpUnstageHunk:
		return file.Change.Area == workspace.GitAreaStaged
	case OpDiscardHunk:
		return file.Change.Area == workspace.GitAreaWorking && file.Comparison.BeforePresent && file.Comparison.AfterPresent
	}
	return false
}

func (document *Document) hunkLayout(file *File, width int) []headerAction {
	fileIndex := -1
	for index := range document.files {
		if &document.files[index] == file {
			fileIndex = index
		}
	}
	if fileIndex < 0 {
		return nil
	}
	var actions []headerAction
	for _, candidate := range []headerAction{{op: OpStageHunk, label: "Stage hunk"}, {op: OpUnstageHunk, label: "Unstage hunk"}, {op: OpDiscardHunk, label: "Discard hunk"}} {
		if document.hunkAllowed(fileIndex, candidate.op) {
			actions = append(actions, candidate)
		}
	}
	x := width
	for index := len(actions) - 1; index >= 0; index-- {
		w := rendering.VisibleWidth(actions[index].label) + 4
		if x-w < width/2 {
			return actions[index+1:]
		}
		x -= w
		actions[index].x, actions[index].w = x, w
		x-- // one column between buttons
	}
	return actions
}

func (document *Document) paintHunk(buf *surface.CellBuffer, x, y, index int) {
	tokens := document.tokens
	r := document.rows.At(index)
	b := document.files[r.file].blocks[r.block]
	bg := tokens.Color(ui.ColorBg)
	hovered := document.hovered && document.hoverRow == index
	current := false
	if fileIndex, blockIndex, ok := document.currentHunk(); ok && fileIndex == r.file && blockIndex == r.block {
		current = true
	}
	text := fmt.Sprintf("@@ −%d,%d +%d,%d @@", b.oldStart+1, b.oldEnd-b.oldStart, b.newStart+1, b.newEnd-b.newStart)
	buf.WriteString(x+1, y, rendering.TruncateVisual(text, max(0, document.body.W/2-2), "…"), tokens.Color(ui.ColorDiffHunk), bg, 0)
	for _, action := range document.hunkLayout(&document.files[r.file], document.body.W) {
		ink, fill := tokens.Color(ui.ColorMuted), bg
		if hovered || current {
			ink = tokens.Color(ui.ColorAccent)
			if action.op == OpDiscardHunk {
				ink = tokens.Color(ui.ColorError)
			}
		}
		if hovered && document.hoverX >= action.x && document.hoverX < action.x+action.w {
			fill = tokens.Color(ui.ColorSelection)
			ink = cell.ContrastColor(fill)
		}
		buf.WriteString(x+action.x, y, "[ "+action.label+" ]", ink, fill, 0)
	}
}

// currentHunk is the hunk under the top of the reading position, or the next
// one below it: the target of the hunk keys.
func (document *Document) currentHunk() (int, int, bool) {
	start := document.scroll.Offset()
	if document.stickyFile() >= 0 {
		start++
	}
	for index := start; index < document.rows.Len() && index < start+max(1, document.body.H); index++ {
		r := document.rows.At(index)
		if r.kind == rowHunk || r.kind == rowLine && !r.inGap {
			return r.file, r.block, true
		}
	}
	return 0, 0, false
}

// ---- selection --------------------------------------------------------------

func (document *Document) lineText(rowIndex, side int) (string, bool) {
	if rowIndex < 0 || rowIndex >= document.rows.Len() || document.rows.At(rowIndex).kind != rowLine {
		return "", false
	}
	r := document.rows.At(rowIndex)
	file := &document.files[r.file]
	switch {
	case side == sideOld || side == sideUnified && r.lk == lineDeleted:
		if r.old > 0 && r.old <= len(file.before) {
			return file.before[r.old-1], true
		}
	case r.new > 0 && r.new <= len(file.after):
		return file.after[r.new-1], true
	}
	return "", false
}

func (document *Document) sourceLine(rowIndex, side int) (diffview.SourceLines, int) {
	if rowIndex < 0 || rowIndex >= document.rows.Len() || document.rows.At(rowIndex).kind != rowLine {
		return diffview.SourceLines{}, 0
	}
	r := document.rows.At(rowIndex)
	file := &document.files[r.file]
	if side == sideOld || side == sideUnified && r.lk == lineDeleted {
		return file.beforeSource, r.old
	}
	return file.afterSource, r.new
}

func (document *Document) sideAt(localX int) int {
	if document.presented != Split {
		return sideUnified
	}
	if localX <= (document.body.W-1)/2 {
		return sideOld
	}
	return sideNew
}

func (document *Document) columnAt(localX, side int) int {
	switch side {
	case sideOld:
		return max(0, localX-document.splitGutter()+document.hscroll)
	case sideNew:
		return max(0, localX-(document.body.W-1)/2-1-document.splitGutter()+document.hscroll)
	}
	return max(0, localX-document.unifiedGutter()+document.hscroll)
}

func (document *Document) beginDrag(ctx ui.EventContext, rowIndex int) {
	side := document.sideAt(ctx.LocalX)
	at := point{row: rowIndex, column: document.columnAt(ctx.LocalX, side)}
	document.selection = selection{anchor: at, end: at, side: side, active: true, dragging: true}
}

func (document *Document) dragTo(ctx ui.EventContext) {
	if ctx.LocalY <= 0 {
		document.scroll.ScrollBy(-1)
	} else if ctx.LocalY >= document.body.H-1 {
		document.scroll.ScrollBy(1)
	}
	rowIndex := max(0, min(document.rows.Len()-1, document.scroll.Offset()+max(0, ctx.LocalY)))
	document.selection.end = point{row: rowIndex, column: document.columnAt(ctx.LocalX, document.selection.side)}
}

func (document *Document) clearSelection() { document.selection = selection{} }

// CancelPointerCapture drops an interrupted drag without copying.
func (document *Document) CancelPointerCapture() { document.clearSelection() }

func (document *Document) ordered() (point, point) {
	a, b := document.selection.anchor, document.selection.end
	if a.row > b.row || a.row == b.row && a.column > b.column {
		a, b = b, a
	}
	return a, b
}

func (document *Document) selected(rowIndex, side, start, end int) bool {
	if !document.selection.active || side != document.selection.side {
		return false
	}
	a, b := document.ordered()
	return rowIndex >= a.row && rowIndex <= b.row && (rowIndex > a.row || end > a.column) && (rowIndex < b.row || start < b.column)
}

// SelectedText is the selected source text, one line per selected row, with
// gutters, markers and absent alignment rows excluded.
func (document *Document) SelectedText() string {
	if !document.selection.active {
		return ""
	}
	a, b := document.ordered()
	var parts []string
	for rowIndex := a.row; rowIndex <= b.row && rowIndex < document.rows.Len(); rowIndex++ {
		_, ok := document.lineText(rowIndex, document.selection.side)
		if !ok {
			continue
		}
		from, to := 0, 1<<30
		if rowIndex == a.row {
			from = a.column
		}
		if rowIndex == b.row {
			to = b.column
		}
		var line strings.Builder
		source, number := document.sourceLine(rowIndex, document.selection.side)
		for unit := range source.Range(number, from, to) {
			if unit.Column+unit.Width > from && unit.Column < to {
				line.WriteString(unit.Raw)
			}
		}
		parts = append(parts, line.String())
	}
	if len(parts) == 1 && parts[0] == "" {
		return ""
	}
	return strings.Join(parts, "\n")
}

func (document *Document) nextLineRow(from, direction int) int {
	for index := from; index >= 0 && index < document.rows.Len(); index += direction {
		if document.rows.At(index).kind == rowLine {
			return index
		}
	}
	return -1
}

// extendSelection grows a whole-line selection with Shift+Up/Down.
func (document *Document) extendSelection(direction int) {
	side := sideUnified
	if document.presented == Split {
		side = sideNew
	}
	if !document.selection.active {
		first := document.nextLineRow(document.scroll.Offset()+1, 1)
		if first < 0 {
			return
		}
		document.selection = selection{anchor: point{row: first}, end: point{row: first, column: 1 << 30}, side: side, active: true}
		return
	}
	next := document.nextLineRow(document.selection.end.row+direction, direction)
	if next < 0 {
		return
	}
	document.selection.end = point{row: next, column: 1 << 30}
	if next < document.selection.anchor.row {
		document.selection.end.column = 0
		document.selection.anchor.column = 1 << 30
	} else {
		document.selection.anchor.column = 0
	}
	document.scroll.EnsureVisible(next)
}

// selectCurrentFile selects every shown line of the current file (Ctrl+A).
func (document *Document) selectCurrentFile() {
	current := document.currentFileIndex()
	if current < 0 {
		return
	}
	first, last := -1, -1
	for index, r := range document.rows.All() {
		if r.file == current && r.kind == rowLine {
			if first < 0 {
				first = index
			}
			last = index
		}
	}
	if first < 0 {
		return
	}
	side := sideUnified
	if document.presented == Split {
		side = sideNew
	}
	document.selection = selection{anchor: point{row: first}, end: point{row: last, column: 1 << 30}, side: side, active: true}
}

// ---- find -------------------------------------------------------------------

// SetQuery finds every case-insensitive occurrence in the shown lines and
// reveals the first.
func (document *Document) SetQuery(query string) {
	document.search.Query = query
	document.rebuildMatches()
	if len(document.search.Matches) > 0 {
		document.search.Active = 0
		document.revealMatch()
	}
}

func (document *Document) Query() string { return document.search.Query }

func (document *Document) rebuildMatches() {
	if document.geometryState != "" {
		document.search.Suspend()
		return
	}
	if document.search.Query == "" {
		document.search.Reset("", nil)
		return
	}
	rows := document.rows
	rows.spans = append([]rowSpan(nil), rows.spans...)
	files := document.files
	sides := []int{sideUnified}
	if document.presented == Split {
		sides = []int{sideOld, sideNew}
	}
	document.search.Reset(document.search.Query, func(yield func(diffview.SearchLine) bool) {
		for rowIndex, r := range rows.All() {
			if r.kind != rowLine {
				continue
			}
			for _, side := range sides {
				file := &files[r.file]
				text, ok := "", false
				if side == sideOld || side == sideUnified && r.lk == lineDeleted {
					if r.old > 0 && r.old <= len(file.before) {
						text, ok = file.before[r.old-1], true
					}
				} else if r.new > 0 && r.new <= len(file.after) {
					text, ok = file.after[r.new-1], true
				}
				if !ok {
					continue
				}
				if !yield(diffview.SearchLine{Row: rowIndex, Side: side, Text: text}) {
					return
				}
			}
		}
	})
}

func (document *Document) matchAt(rowIndex, side, start, end int) (cell.Color, bool, bool) {
	for _, index := range document.search.ByRow[rowIndex] {
		m := document.search.Matches[index]
		if m.Side == side && start < m.End && end > m.Start {
			if index == document.search.Active {
				return document.tokens.Color(ui.ColorAccent), true, true
			}
			return document.tokens.Color(ui.ColorWarning), false, true
		}
	}
	return cell.NoColor, false, false
}

// NextMatch cycles through matches and reveals the active one.
func (document *Document) NextMatch(previous bool) bool {
	count := len(document.search.Matches)
	if count == 0 {
		return false
	}
	step := 1
	if previous {
		step = -1
	}
	document.search.Active = (document.search.Active + step + count) % count
	document.revealMatch()
	return true
}

func (document *Document) revealMatch() {
	if document.search.Active < 0 || document.search.Active >= len(document.search.Matches) {
		return
	}
	m := document.search.Matches[document.search.Active]
	offset := document.scroll.Offset()
	if m.Row <= offset || m.Row >= offset+document.body.H {
		document.scroll.ScrollTo(max(0, m.Row-2))
	}
	if m.Start < document.hscroll || m.End > document.hscroll+document.textWidth {
		document.hscroll = max(0, min(m.Start-4, document.maxWidth-document.textWidth))
	}
}

// SearchStatus summarizes the find state for the owner's status line.
func (document *Document) SearchStatus() string {
	return document.search.Status()
}

func (document *Document) SearchCommand() tea.Cmd {
	return document.search.Command(document.revealMatch)
}
func (document *Document) SearchDone() <-chan struct{} { return document.search.Done() }
func (document *Document) StopSearch()                 { document.search.Cancel() }
func (document *Document) WaitSearch()                 { document.search.Wait() }
