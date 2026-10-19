package gitdiff

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/diffview"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

// ActionFile carries a FileIntent from a header action or document key.
const ActionFile = "gitdiff.file"

// Op is a per-file intent; the owner decides how (and whether) to run it.
type Op string

const (
	OpStage   Op = "stage"
	OpUnstage Op = "unstage"
	OpDiscard Op = "discard"
	OpOpen    Op = "open"

	OpStageHunk   Op = "stage-hunk"
	OpUnstageHunk Op = "unstage-hunk"
	OpDiscardHunk Op = "discard-hunk"
)

// FileIntent identifies the exact prepared comparison an intent targets, so
// the owner can fail closed when the file changed after it was painted.
type FileIntent struct {
	Op         Op
	Key        string
	Change     workspace.GitChange
	Comparison workspace.GitComparison
	Historical bool
	Line       int // one-based new-side line for OpOpen; zero = first change
	// Hunk is the displayed hunk a *Hunk op targets, in comparison lines.
	Hunk workspace.GitHunkRange
}

// Presentation selects one stream of lines or aligned old/new columns.
type Presentation string

const (
	Unified Presentation = "unified"
	Split   Presentation = "split"
)

// splitMinWidth is the narrowest document that still shows two columns.
const splitMinWidth = 100

type rowKind uint8

const (
	rowHeader rowKind = iota
	rowHunk
	rowLine
	rowGap
	rowNote
	rowSpacer
)

type row struct {
	kind     rowKind
	file     int
	block    int // hunk: block index; gap: block it precedes (len = trailing)
	old, new int
	lk       lineKind
	count    int
	inGap    bool // an expanded unchanged line between hunks
}

type headerAction struct {
	op    Op
	label string
	x, w  int
}

// Document is the scrollable multi-file comparison.
type Document struct {
	ui.Base
	tokens        *ui.TokenSet
	ink           diffview.SyntaxInk
	files         []File
	rows          rowIndex
	headerRows    []int // file index → row of its header
	scroll        *ui.ScrollModel
	scrollbar     *ui.ScrollbarControl
	hscroll       int
	textWidth     int
	maxWidth      int
	preferred     Presentation
	presented     Presentation
	readOnly      bool
	collapsed     map[string]bool
	expanded      map[string]bool
	empty         string
	numberW       int
	hoverRow      int
	hoverX        int
	hovered       bool
	lastClickRow  int
	lastClickAt   time.Time
	body          ui.Rect
	selection     selection
	search        diffview.SearchProjection
	geometryState string
}

// New creates an empty document.
func New(id ui.ComponentID, tokens *ui.TokenSet) *Document {
	document := &Document{
		Base: ui.NewBase(id), tokens: tokens, scroll: ui.NewScrollModel(),
		preferred: Unified, presented: Unified,
		collapsed: make(map[string]bool), expanded: make(map[string]bool),
		empty: "No changes", hoverRow: -1, lastClickRow: -1,
	}
	document.scrollbar = ui.NewScrollbarControl(ui.NewID(id, "scrollbar"), document.scroll, tokens)
	document.scrollbar.SetSurfaceBackground(ui.ColorBg)
	return document
}

func (document *Document) SetTokens(tokens *ui.TokenSet) {
	document.tokens = tokens
	document.scrollbar.SetTokens(tokens)
}

// SetReadOnly hides stage/unstage/discard (history and commit evidence).
func (document *Document) SetReadOnly(readOnly bool) { document.readOnly = readOnly }

// SetEmptyText is shown when there are no files.
func (document *Document) SetEmptyText(text string) { document.empty = text }

// Files returns the prepared files in display order.
func (document *Document) Files() []File { return document.files }

// SetFiles installs a new prepared set. Collapse/expand state and the reading
// position survive when the same file is still present.
func (document *Document) SetFiles(files []File) {
	if sameFiles(document.files, files) {
		// A re-read that found identical comparisons keeps the reading
		// position, selection and find state untouched.
		document.files = files
		state, width := document.sourceGeometryStatus(), 0
		for _, file := range files {
			width = max(width, file.beforeSource.Width(), file.afterSource.Width())
		}
		if width != document.maxWidth || state != document.geometryState {
			document.geometryState, document.maxWidth = state, width
			document.rebuildMatches()
		}
		return
	}
	anchorKey, anchorDelta := "", 0
	if current := document.currentFileIndex(); current >= 0 {
		anchorKey = document.files[current].Key
		anchorDelta = document.scroll.Offset() - document.headerRows[current]
	}
	document.files = files
	document.geometryState = document.sourceGeometryStatus()
	document.maxWidth, document.numberW = 0, 3
	for index := range files {
		document.maxWidth = max(document.maxWidth, files[index].beforeSource.Width(), files[index].afterSource.Width())
		document.numberW = max(document.numberW, len(fmt.Sprint(max(len(files[index].before), len(files[index].after)))))
	}
	document.rebuild()
	if anchorKey != "" {
		for index := range document.files {
			if document.files[index].Key == anchorKey {
				document.scroll.ScrollTo(document.headerRows[index] + anchorDelta)
				return
			}
		}
	}
}

func (document *Document) SourceGeometry() []diffview.SourceLines {
	sources := make([]diffview.SourceLines, 0, 2*len(document.files))
	for _, file := range document.files {
		sources = append(sources, file.beforeSource, file.afterSource)
	}
	return sources
}

func (document *Document) InvalidateWidthProfile() {
	document.geometryState = document.sourceGeometryStatus()
	document.maxWidth = 0
	for _, file := range document.files {
		document.maxWidth = max(document.maxWidth, file.beforeSource.Width(), file.afterSource.Width())
	}
	document.rebuildMatches()
}

func (document *Document) BeginGeometryReflow() {
	document.geometryState = "Calibrating comparison geometry…"
	document.clearSelection()
	document.search.Suspend()
}

func (document *Document) sourceGeometryStatus() string {
	for _, file := range document.files {
		if status := file.beforeSource.GeometryStatus(); status != "" {
			return status
		}
		if status := file.afterSource.GeometryStatus(); status != "" {
			return status
		}
	}
	return ""
}

func sameFiles(a, b []File) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	for index := range a {
		x, y := &a[index], &b[index]
		if x.Key != y.Key || x.Note != y.Note || x.Comparison.Fingerprint != y.Comparison.Fingerprint ||
			x.Comparison.Before != y.Comparison.Before || x.Comparison.After != y.Comparison.After {
			return false
		}
	}
	return true
}

func (document *Document) SetPresentation(presentation Presentation) {
	if presentation == Unified || presentation == Split {
		document.preferred = presentation
		document.rebuild()
	}
}

// Presentation is the requested one; Presented is what fits the width.
func (document *Document) Presentation() Presentation { return document.preferred }
func (document *Document) Presented() Presentation    { return document.presented }

// Stats totals the prepared set.
func (document *Document) Stats() (files, added, removed int) {
	for index := range document.files {
		added += document.files[index].Added
		removed += document.files[index].Removed
	}
	return len(document.files), added, removed
}

// AllCollapsed reports whether every file shows only its header.
func (document *Document) AllCollapsed() bool {
	for index := range document.files {
		if !document.collapsed[document.files[index].Key] {
			return false
		}
	}
	return len(document.files) > 0
}

// SetAllCollapsed folds or unfolds every file.
func (document *Document) SetAllCollapsed(collapsed bool) {
	for index := range document.files {
		document.collapsed[document.files[index].Key] = collapsed
	}
	document.rebuild()
}

// RevealFile scrolls the file's header to the top.
func (document *Document) RevealFile(key string) bool {
	for index := range document.files {
		if document.files[index].Key == key {
			document.scroll.ScrollTo(document.headerRows[index])
			return true
		}
	}
	return false
}

// CurrentFile is the file owning the top visible row (the sticky header).
func (document *Document) CurrentFile() (File, bool) {
	index := document.currentFileIndex()
	if index < 0 {
		return File{}, false
	}
	return document.files[index], true
}

func (document *Document) currentFileIndex() int {
	if document.rows.Len() == 0 || len(document.files) == 0 {
		return -1
	}
	offset := min(document.scroll.Offset(), document.rows.Len()-1)
	return document.rows.At(offset).file
}

// NavigateFile moves the next/previous file header to the top.
func (document *Document) NavigateFile(direction int) bool {
	if len(document.files) == 0 {
		return false
	}
	current := max(0, document.currentFileIndex())
	offset := document.scroll.Offset()
	target := current + direction
	if direction < 0 && offset > document.headerRows[current] {
		target = current
	}
	target = max(0, min(len(document.files)-1, target))
	document.scroll.ScrollTo(document.headerRows[target])
	return true
}

// NavigateHunk moves the next/previous hunk header just below the sticky header.
func (document *Document) NavigateHunk(direction int) bool {
	offset := document.scroll.Offset() + 1
	if direction > 0 {
		for index := offset + 1; index < document.rows.Len(); index++ {
			if document.rows.At(index).kind == rowHunk {
				document.scroll.ScrollTo(index - 1)
				return true
			}
		}
		return false
	}
	for index := offset - 1; index >= 0; index-- {
		if document.rows.At(index).kind == rowHunk {
			document.scroll.ScrollTo(max(0, index-1))
			return true
		}
	}
	return false
}

func (document *Document) gapKey(file, gap int) string {
	return fmt.Sprintf("%s#%d", document.files[file].Key, gap)
}

// rebuild derives the flat row index from files, fold state and presentation.
func (document *Document) rebuild() {
	document.rows.reset()
	document.headerRows = document.headerRows[:0]
	split := document.presented == Split
	for fileIndex := range document.files {
		file := &document.files[fileIndex]
		if fileIndex > 0 {
			document.rows.single(row{kind: rowSpacer, file: fileIndex - 1})
		}
		document.headerRows = append(document.headerRows, document.rows.Len())
		document.rows.single(row{kind: rowHeader, file: fileIndex})
		if document.collapsed[file.Key] {
			continue
		}
		if file.Note != "" {
			document.rows.single(row{kind: rowNote, file: fileIndex})
			continue
		}
		oldCursor, newCursor := 0, 0
		for blockIndex, b := range file.blocks {
			document.appendGap(fileIndex, blockIndex, oldCursor, newCursor, b.oldStart)
			document.rows.single(row{kind: rowHunk, file: fileIndex, block: blockIndex})
			if split {
				document.rows.add(rowSpan{count: len(b.pairs), base: row{kind: rowLine, file: fileIndex, block: blockIndex}, pairs: b.pairs})
			} else {
				document.rows.add(rowSpan{count: len(b.lines), base: row{kind: rowLine, file: fileIndex, block: blockIndex}, lines: b.lines})
			}
			oldCursor, newCursor = b.oldEnd, b.newEnd
		}
		if len(file.blocks) > 0 {
			document.appendGap(fileIndex, len(file.blocks), oldCursor, newCursor, len(file.before))
		}
	}
	document.scroll.SetContent(document.rows.Len())
	document.scroll.Clamp()
	if !document.selection.dragging {
		document.clearSelection()
	}
	document.rebuildMatches()
}

func (document *Document) appendGap(fileIndex, gap, oldFrom, newFrom, oldTo int) {
	count := oldTo - oldFrom
	if count <= 0 {
		return
	}
	file := &document.files[fileIndex]
	if !document.expanded[document.gapKey(fileIndex, gap)] {
		document.rows.single(row{kind: rowGap, file: fileIndex, block: gap, count: count})
		return
	}
	document.rows.add(rowSpan{count: min(count, max(0, len(file.after)-newFrom)), base: row{kind: rowLine, file: fileIndex, block: gap, old: oldFrom + 1, new: newFrom + 1, lk: lineContext, inGap: true}, gap: true})
}

func (document *Document) Children() []ui.Component {
	if document.scrollbar.Bounds().Empty() {
		return nil
	}
	return []ui.Component{document.scrollbar}
}

func (document *Document) Measure(_ ui.LayoutContext, constraints ui.Constraints) ui.Size {
	return constraints.ClampSize(ui.Size{W: constraints.MaxW, H: constraints.MaxH})
}

func (document *Document) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	document.SetBounds(bounds)
	presented := Unified
	if document.preferred == Split && bounds.W >= splitMinWidth {
		presented = Split
	}
	if presented != document.presented {
		document.presented = presented
		document.rebuild()
	}
	document.scroll.SetViewport(bounds.H)
	document.scroll.SetContent(document.rows.Len())
	barW := 0
	if document.scrollbar.NeededForViewport(bounds.H) {
		barW = min(ui.ScrollbarWidth, bounds.W)
	}
	document.body = ui.Rect{W: max(0, bounds.W-barW), H: bounds.H}
	document.scrollbar.Layout(ctx, ui.Rect{X: bounds.W - barW, W: barW, H: bounds.H})
	document.textWidth = max(1, document.body.W-document.unifiedGutter())
	if document.presented == Split {
		document.textWidth = max(1, (document.body.W-1)/2-document.splitGutter())
	}
	document.hscroll = max(0, min(document.hscroll, document.maxWidth-document.textWidth))
}

func (document *Document) unifiedGutter() int { return 2*document.numberW + 4 }
func (document *Document) splitGutter() int   { return document.numberW + 3 }

// stickyFile is the file whose header pins to the first row, or -1.
func (document *Document) stickyFile() int {
	offset := document.scroll.Offset()
	if offset >= document.rows.Len() || document.body.H < 3 {
		return -1
	}
	r := document.rows.At(offset)
	if r.kind == rowHeader || r.kind == rowSpacer {
		return -1
	}
	return r.file
}

// rowAt maps a local y to a row index; sticky reports the pinned header.
func (document *Document) rowAt(localY int) (index int, sticky bool) {
	if localY < 0 || localY >= document.body.H {
		return -1, false
	}
	if localY == 0 {
		if file := document.stickyFile(); file >= 0 {
			return document.headerRows[file], true
		}
	}
	index = document.scroll.Offset() + localY
	if index >= document.rows.Len() {
		return -1, false
	}
	return index, false
}

func (document *Document) actionsFor(file *File) []headerAction {
	var actions []headerAction
	add := func(op Op, label string) { actions = append(actions, headerAction{op: op, label: label}) }
	if !document.readOnly && !file.Historical {
		switch file.Change.Area {
		case workspace.GitAreaStaged:
			add(OpUnstage, "Unstage")
		case workspace.GitAreaConflict:
			add(OpStage, "Mark Resolved")
		case workspace.GitAreaUntracked:
			add(OpStage, "Stage")
		default:
			add(OpStage, "Stage")
			add(OpDiscard, "Discard")
		}
	}
	if !file.Comparison.Deleted {
		add(OpOpen, "Open")
	}
	return actions
}

// headerLayout places the trailing stats and actions for one header row.
func (document *Document) headerLayout(file *File, width int) ([]headerAction, int) {
	actions := document.actionsFor(file)
	x := width
	for index := len(actions) - 1; index >= 0; index-- {
		w := rendering.VisibleWidth(actions[index].label) + 4
		if x-w < width/2 {
			actions = actions[index+1:]
			break
		}
		x -= w
		actions[index].x, actions[index].w = x, w
		x-- // one column between buttons
	}
	stats := document.statsText(file)
	statsX := x - rendering.VisibleWidth(stats) - 1
	return actions, statsX
}

func (document *Document) statsText(file *File) string {
	if file.Note != "" {
		return ""
	}
	return fmt.Sprintf("+%d −%d", file.Added, file.Removed)
}

func (document *Document) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	bounds := document.Bounds()
	tokens := document.tokens
	bg := tokens.Color(ui.ColorBg)
	ui.FillRect(buf, originX, originY, bounds, cell.BlankCell(bg))
	x, y := originX+bounds.X, originY+bounds.Y
	if status := document.geometryState; status != "" {
		buf.WriteString(x+1, y, rendering.TruncateVisual(status, max(0, document.body.W-2), "…"), tokens.Color(ui.ColorMuted), bg, 0)
		return
	}
	document.hovered = ctx.Hovered
	if len(document.files) == 0 {
		if bounds.H > 0 {
			buf.WriteString(x+1, y, rendering.TruncateVisual(document.empty, max(0, document.body.W-2), "…"), tokens.Color(ui.ColorMuted), bg, 0)
		}
		return
	}
	sticky := document.stickyFile()
	for local := 0; local < document.body.H; local++ {
		index := document.scroll.Offset() + local
		if local == 0 && sticky >= 0 {
			document.paintHeader(buf, x, y, sticky, document.headerRows[sticky], true)
			continue
		}
		if index >= document.rows.Len() {
			break
		}
		document.paintRow(buf, x, y+local, index)
	}
}

func (document *Document) paintRow(buf *surface.CellBuffer, x, y, index int) {
	r := document.rows.At(index)
	tokens := document.tokens
	bg := tokens.Color(ui.ColorBg)
	width := document.body.W
	switch r.kind {
	case rowHeader:
		document.paintHeader(buf, x, y, r.file, index, false)
	case rowHunk:
		document.paintHunk(buf, x, y, index)
	case rowGap:
		fill := bg
		ink := tokens.Color(ui.ColorMuted)
		if document.hovered && document.hoverRow == index {
			fill, ink = tokens.Color(ui.ColorHover), tokens.Color(ui.ColorFg)
		}
		buf.FillRect(surface.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(fill))
		noun := "lines"
		if r.count == 1 {
			noun = "line"
		}
		text := fmt.Sprintf("⋯  %d unchanged %s · click to expand", r.count, noun)
		buf.WriteString(x+document.unifiedGutterOrSplit(), y, rendering.TruncateVisual(text, max(0, width-document.unifiedGutterOrSplit()), "…"), ink, fill, 0)
	case rowNote:
		buf.WriteString(x+3, y, rendering.TruncateVisual(document.files[r.file].Note, max(0, width-4), "…"), tokens.Color(ui.ColorMuted), bg, cell.AttrItalic)
	case rowLine:
		if document.presented == Split {
			document.paintSplitLine(buf, x, y, index)
		} else {
			document.paintUnifiedLine(buf, x, y, index)
		}
	}
}

func (document *Document) unifiedGutterOrSplit() int {
	if document.presented == Split {
		return document.splitGutter()
	}
	return document.unifiedGutter()
}

func (document *Document) paintHeader(buf *surface.CellBuffer, x, y, fileIndex, rowIndex int, sticky bool) {
	tokens := document.tokens
	file := &document.files[fileIndex]
	width := document.body.W
	fill := tokens.Color(ui.ColorSubPaneBg)
	hovered := document.hovered && document.hoverRow == rowIndex
	if hovered && document.actionAt(rowIndex, document.hoverX) == "" {
		fill = tokens.Color(ui.ColorHover)
	}
	buf.FillRect(surface.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(fill))
	chevron := "▾"
	if document.collapsed[file.Key] {
		chevron = "▸"
	}
	col := 1
	buf.WriteString(x+col, y, chevron, tokens.Color(ui.ColorMuted), fill, 0)
	col += 2
	code := file.Code()
	buf.WriteString(x+col, y, code, tokens.Color(CodeRole(code, file.Change.Area)), fill, cell.AttrBold)
	col += rendering.VisibleWidth(code) + 1
	actions, statsX := document.headerLayout(file, width)
	limit := max(col, statsX-1)
	name, dir := SplitPath(file.Path())
	nameText := rendering.TruncateVisual(name, max(0, limit-col), "…")
	buf.WriteString(x+col, y, nameText, tokens.Color(ui.ColorFg), fill, cell.AttrBold)
	col += rendering.VisibleWidth(nameText) + 2
	detail := dir
	if file.Comparison.OriginalPath != "" {
		detail = strings.TrimSpace(dir + "  ← " + file.Comparison.OriginalPath)
	}
	if area := areaLabel(file); area != "" {
		detail = strings.TrimSpace(detail + "  · " + area)
	}
	if col < limit && detail != "" {
		buf.WriteString(x+col, y, rendering.TruncateVisual(detail, limit-col, "…"), tokens.Color(ui.ColorMuted), fill, 0)
	}
	if stats := document.statsText(file); stats != "" && statsX > col-2 {
		added, removed, _ := strings.Cut(stats, " ")
		buf.WriteString(x+statsX, y, added, tokens.Color(ui.ColorDiffAddition), fill, 0)
		buf.WriteString(x+statsX+rendering.VisibleWidth(added)+1, y, removed, tokens.Color(ui.ColorDiffDeletion), fill, 0)
	}
	for _, action := range actions {
		ink, actionFill := tokens.Color(ui.ColorAccent), fill
		if hovered && document.hoverX >= action.x && document.hoverX < action.x+action.w {
			actionFill = tokens.Color(ui.ColorSelection)
			ink = cell.ContrastColor(actionFill)
		}
		if action.op == OpDiscard {
			ink = tokens.Color(ui.ColorError)
			if actionFill != fill {
				ink = cell.ContrastColor(actionFill)
			}
		}
		buf.WriteString(x+action.x, y, "[ "+action.label+" ]", ink, actionFill, 0)
	}
}

func areaLabel(file *File) string {
	if file.Historical {
		return ""
	}
	switch file.Change.Area {
	case workspace.GitAreaStaged:
		return "staged"
	case workspace.GitAreaUntracked:
		return "untracked"
	case workspace.GitAreaConflict:
		return "conflict"
	}
	return ""
}

// CodeRole colors a status letter consistently across the panel and review.
func CodeRole(code string, area workspace.GitChangeArea) ui.SemanticColor {
	if area == workspace.GitAreaConflict {
		return ui.ColorError
	}
	switch code {
	case "A", "U", "?":
		return ui.ColorSuccess
	case "D":
		return ui.ColorError
	case "R", "C":
		return ui.ColorAccent
	}
	return ui.ColorWarning
}

func (document *Document) lineColors(kind lineKind) (cell.Color, ui.SemanticColor, string) {
	tokens := document.tokens
	switch kind {
	case lineAdded:
		return tokens.Color(ui.ColorDiffAdditionBg), ui.ColorDiffAddition, "+"
	case lineDeleted:
		return tokens.Color(ui.ColorDiffDeletionBg), ui.ColorDiffDeletion, "−"
	}
	return tokens.Color(ui.ColorBg), ui.ColorMuted, " "
}

func (document *Document) paintUnifiedLine(buf *surface.CellBuffer, x, y, index int) {
	r := document.rows.At(index)
	file := &document.files[r.file]
	fill, markRole, mark := document.lineColors(r.lk)
	buf.FillRect(surface.Rect{X: x, Y: y, W: document.body.W, H: 1}, cell.BlankCell(fill))
	old, next := "", ""
	if r.old > 0 && r.lk != lineAdded {
		old = fmt.Sprint(r.old)
	}
	if r.new > 0 && r.lk != lineDeleted {
		next = fmt.Sprint(r.new)
	}
	muted := document.tokens.Color(ui.ColorMuted)
	buf.WriteString(x, y, fmt.Sprintf("%*s %*s ", document.numberW, old, document.numberW, next), muted, fill, 0)
	buf.WriteString(x+2*document.numberW+2, y, mark, document.tokens.Color(markRole), fill, cell.AttrBold)
	text, spans, source := file.after, file.afterSyntax, file.afterSource
	number := r.new
	if r.lk == lineDeleted {
		text, spans, source, number = file.before, file.beforeSyntax, file.beforeSource, r.old
	}
	document.paintSource(buf, x+document.unifiedGutter(), y, document.textWidth, text, source, spans, number, fill, index, sideUnified)
}

func (document *Document) paintSplitLine(buf *surface.CellBuffer, x, y, index int) {
	r := document.rows.At(index)
	file := &document.files[r.file]
	half := (document.body.W - 1) / 2
	muted := document.tokens.Color(ui.ColorMuted)
	side := func(left int, number int, lines []string, source diffview.SourceLines, spans [][]diffview.SyntaxSpan, kind lineKind, which int) {
		fill, markRole, mark := document.lineColors(kind)
		if number == 0 {
			fill = document.tokens.Color(ui.ColorSubPaneBg)
			mark = " "
		}
		buf.FillRect(surface.Rect{X: left, Y: y, W: half, H: 1}, cell.BlankCell(fill))
		if number == 0 {
			return
		}
		buf.WriteString(left, y, fmt.Sprintf("%*d ", document.numberW, number), muted, fill, 0)
		buf.WriteString(left+document.numberW+1, y, mark, document.tokens.Color(markRole), fill, cell.AttrBold)
		document.paintSource(buf, left+document.splitGutter(), y, max(0, half-document.splitGutter()), lines, source, spans, number, fill, index, which)
	}
	oldKind, newKind := lineContext, lineContext
	if r.lk != lineContext {
		oldKind, newKind = lineDeleted, lineAdded
	}
	side(x, r.old, file.before, file.beforeSource, file.beforeSyntax, oldKind, sideOld)
	buf.WriteString(x+half, y, "│", document.tokens.Color(ui.ColorBorder), document.tokens.Color(ui.ColorBg), 0)
	side(x+half+1, r.new, file.after, file.afterSource, file.afterSyntax, newKind, sideNew)
}

func (document *Document) paintSource(buf *surface.CellBuffer, x, y, width int, lines []string, source diffview.SourceLines, syntax [][]diffview.SyntaxSpan, number int, fill cell.Color, rowIndex, side int) {
	if number <= 0 || number > len(lines) || width <= 0 {
		return
	}
	var spans []diffview.SyntaxSpan
	if number <= len(syntax) {
		spans = syntax[number-1]
	}
	base := document.tokens.Color(ui.ColorFg)
	spanIndex := 0
	cursor := source.Iterator(number, document.hscroll, document.hscroll+width)
	for unit, ok := cursor.Next(); ok; unit, ok = cursor.Next() {
		left := unit.Column - document.hscroll
		if left < 0 {
			continue
		}
		if left+unit.Width > width {
			break
		}
		fg := base
		for spanIndex < len(spans) && spans[spanIndex].End <= unit.Offset {
			spanIndex++
		}
		if spanIndex < len(spans) && spans[spanIndex].Start <= unit.Offset {
			if ink, ok := document.ink.Ink(document.tokens, spans[spanIndex].Kind); ok {
				fg = ink
			}
		}
		unitFill := fill
		if matchFill, active, ok := document.matchAt(rowIndex, side, unit.Column, unit.Column+unit.Width); ok {
			unitFill = matchFill
			fg = cell.ContrastInk(unitFill)
			_ = active
		}
		if document.selected(rowIndex, side, unit.Column, unit.Column+unit.Width) {
			unitFill = document.tokens.Color(ui.ColorTextSelection)
			fg = document.tokens.Color(ui.ColorTextSelectionFg)
		}
		buf.WriteString(x+left, y, unit.Display, fg, unitFill, 0)
	}
}

func (document *Document) actionAt(rowIndex, localX int) Op {
	if rowIndex < 0 || rowIndex >= document.rows.Len() {
		return ""
	}
	var actions []headerAction
	switch document.rows.At(rowIndex).kind {
	case rowHeader:
		actions, _ = document.headerLayout(&document.files[document.rows.At(rowIndex).file], document.body.W)
	case rowHunk:
		actions = document.hunkLayout(&document.files[document.rows.At(rowIndex).file], document.body.W)
	default:
		return ""
	}
	for _, action := range actions {
		if localX >= action.x && localX < action.x+action.w {
			return action.op
		}
	}
	return ""
}

// hunkIntent targets one displayed hunk of a file.
func (document *Document) hunkIntent(op Op, fileIndex, blockIndex int) *ui.Action {
	action := document.intent(op, fileIndex, 0)
	if action == nil || blockIndex < 0 || blockIndex >= len(document.files[fileIndex].blocks) {
		return nil
	}
	b := document.files[fileIndex].blocks[blockIndex]
	intent := action.Payload.(FileIntent)
	intent.Hunk = workspace.GitHunkRange{OldStart: b.oldStart, OldEnd: b.oldEnd, NewStart: b.newStart, NewEnd: b.newEnd}
	action.Payload = intent
	return action
}

func (document *Document) intent(op Op, fileIndex, line int) *ui.Action {
	file := &document.files[fileIndex]
	if op == OpOpen && file.Comparison.Deleted {
		return nil
	}
	if op != OpOpen && (document.readOnly || file.Historical) {
		return nil
	}
	return &ui.Action{ID: ActionFile, Payload: FileIntent{
		Op: op, Key: file.Key, Change: file.Change, Comparison: file.Comparison,
		Historical: file.Historical, Line: line,
	}}
}

func (document *Document) toggleFile(fileIndex int) {
	key := document.files[fileIndex].Key
	document.collapsed[key] = !document.collapsed[key]
	document.rebuild()
	document.scroll.ScrollTo(min(document.scroll.Offset(), document.headerRows[fileIndex]))
}

func (document *Document) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if document.geometryState != "" {
		return ui.EventResult{Handled: true}
	}
	paint := ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	if evt.IsKey() {
		return document.handleKey(evt.Key)
	}
	switch evt.Kind {
	case ui.EventMouseMotion:
		if document.selection.dragging {
			document.dragTo(ctx)
			return paint
		}
		index, _ := document.rowAt(ctx.LocalY)
		if index != document.hoverRow || ctx.LocalX != document.hoverX {
			document.hoverRow, document.hoverX = index, ctx.LocalX
			return paint
		}
		return ui.EventResult{Handled: true}
	case ui.EventMouseWheel:
		switch evt.MouseWheel.Button {
		case tea.MouseWheelLeft:
			document.scrollHorizontal(-4)
		case tea.MouseWheelRight:
			document.scrollHorizontal(4)
		case tea.MouseWheelUp:
			if evt.MouseWheel.Mod.Contains(tea.ModShift) {
				document.scrollHorizontal(-4)
			} else {
				document.scroll.ScrollBy(-3)
			}
		case tea.MouseWheelDown:
			if evt.MouseWheel.Mod.Contains(tea.ModShift) {
				document.scrollHorizontal(4)
			} else {
				document.scroll.ScrollBy(3)
			}
		}
		return paint
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		index, _ := document.rowAt(ctx.LocalY)
		if index < 0 {
			return ui.EventResult{Handled: true}
		}
		now := time.Now()
		double := index == document.lastClickRow && now.Sub(document.lastClickAt) <= 400*time.Millisecond
		document.lastClickRow, document.lastClickAt = index, now
		r := document.rows.At(index)
		document.clearSelection()
		switch r.kind {
		case rowHunk:
			if op := document.actionAt(index, ctx.LocalX); op != "" {
				paint.Action = document.hunkIntent(op, r.file, r.block)
			}
			return paint
		case rowHeader:
			if op := document.actionAt(index, ctx.LocalX); op != "" {
				paint.Action = document.intent(op, r.file, 0)
				return paint
			}
			document.toggleFile(r.file)
			paint.Invalidate |= ui.InvalidateLayout
		case rowGap:
			document.expanded[document.gapKey(r.file, r.block)] = true
			document.rebuild()
			paint.Invalidate |= ui.InvalidateLayout
		case rowLine:
			if !double {
				document.beginDrag(ctx, index)
			}
			if double {
				line := r.new
				if line == 0 || r.lk == lineDeleted && document.presented == Unified {
					line = max(1, min(r.old, len(document.files[r.file].after)))
				}
				paint.Action = document.intent(OpOpen, r.file, line)
			}
		}
		return paint
	case ui.EventMouseRelease:
		if !document.selection.dragging {
			return ui.EventResult{}
		}
		document.dragTo(ctx)
		document.selection.dragging = false
		text := document.SelectedText()
		document.clearSelection()
		if text != "" {
			paint.Cmd = rendering.CopyToClipboardCmd(text)
		}
		return paint
	}
	return ui.EventResult{}
}

func (document *Document) scrollHorizontal(delta int) {
	document.hscroll = max(0, min(document.hscroll+delta, document.maxWidth-document.textWidth))
}

func (document *Document) handleKey(key tea.KeyPressMsg) ui.EventResult {
	paint := ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	current := document.currentFileIndex()
	if ui.IsCopyChord(key) {
		if !document.selection.active {
			return ui.EventResult{}
		}
		paint.Cmd = rendering.CopyToClipboardCmd(document.SelectedText())
		return paint
	}
	switch key.String() {
	case "shift+up", "shift+down":
		document.extendSelection(map[bool]int{true: -1, false: 1}[key.String() == "shift+up"])
		return paint
	case "ctrl+a":
		document.selectCurrentFile()
		return paint
	case "esc":
		if !document.selection.active {
			return ui.EventResult{}
		}
		document.clearSelection()
		return paint
	case "f3":
		document.NextMatch(false)
		return paint
	case "shift+f3":
		document.NextMatch(true)
		return paint
	case "+", "=":
		if fileIndex, blockIndex, ok := document.currentHunk(); ok {
			op := OpStageHunk
			if document.files[fileIndex].Change.Area == workspace.GitAreaStaged {
				op = OpUnstageHunk
			}
			if document.hunkAllowed(fileIndex, op) {
				paint.Action = document.hunkIntent(op, fileIndex, blockIndex)
			}
		}
		return paint
	case "-":
		if fileIndex, blockIndex, ok := document.currentHunk(); ok && document.hunkAllowed(fileIndex, OpUnstageHunk) {
			paint.Action = document.hunkIntent(OpUnstageHunk, fileIndex, blockIndex)
		}
		return paint
	case "x":
		if fileIndex, blockIndex, ok := document.currentHunk(); ok && document.hunkAllowed(fileIndex, OpDiscardHunk) {
			paint.Action = document.hunkIntent(OpDiscardHunk, fileIndex, blockIndex)
		}
		return paint
	case "up", "k":
		document.scroll.ScrollBy(-1)
	case "down", "j":
		document.scroll.ScrollBy(1)
	case "pgup":
		document.scroll.ScrollBy(-max(1, document.scroll.Viewport()-2))
	case "pgdown", "space":
		document.scroll.ScrollBy(max(1, document.scroll.Viewport()-2))
	case "home", "ctrl+home", "g":
		document.scroll.ScrollTo(0)
	case "end", "ctrl+end", "G":
		document.scroll.ScrollTo(document.scroll.MaxOffset())
	case "left", "h":
		document.scrollHorizontal(-4)
	case "right", "l":
		document.scrollHorizontal(4)
	case "n", "alt+down":
		document.NavigateHunk(1)
	case "p", "alt+up":
		document.NavigateHunk(-1)
	case "]", "alt+right":
		document.NavigateFile(1)
	case "[", "alt+left":
		document.NavigateFile(-1)
	case "z":
		if current >= 0 {
			document.toggleFile(current)
			paint.Invalidate |= ui.InvalidateLayout
		}
	case "Z":
		document.SetAllCollapsed(!document.AllCollapsed())
		paint.Invalidate |= ui.InvalidateLayout
	case "enter", "o":
		if current >= 0 {
			paint.Action = document.intent(OpOpen, current, document.files[current].FirstChangedLine())
		}
	case "s":
		if current >= 0 {
			op := OpStage
			if document.files[current].Change.Area == workspace.GitAreaStaged {
				op = OpUnstage
			}
			paint.Action = document.intent(op, current, 0)
		}
	case "u":
		if current >= 0 {
			paint.Action = document.intent(OpUnstage, current, 0)
		}
	case "d":
		if current >= 0 {
			paint.Action = document.intent(OpDiscard, current, 0)
		}
	default:
		return ui.EventResult{}
	}
	return paint
}

// ScrollOffset exposes the first painted row for tests and anchoring.
func (document *Document) ScrollOffset() int { return document.scroll.Offset() }

func (document *Document) Semantics() ui.Semantics {
	files, added, removed := document.Stats()
	value := ""
	if file, ok := document.CurrentFile(); ok {
		value = file.Path()
	}
	return ui.Semantics{Role: ui.RoleGroup, Label: "Changes", Value: value, Description: fmt.Sprintf("%d files, %d additions, %d deletions", files, added, removed)}
}

var _ ui.Component = (*Document)(nil)
var _ ui.Parent = (*Document)(nil)
