package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// maxKeyValueRows caps the rows a grid paints before it scrolls. Header sets
// are short; the cap keeps an accidental paste of fifty rows from pushing the
// rest of the form off screen.
const maxKeyValueRows = 6

// compactKeyValueRows caps the same table for docked (compact) forms, which
// share their rows with the chat surface.
const compactKeyValueRows = 3

// keyValueGutter is the right-hand column reserved for the row-remove
// affordance. The gutter keeps a removed row from swallowing value text.
const keyValueGutter = 3

// Column captions and the add-row affordance. They are plain text labels, not
// glyphs: the app has no dedicated add icon and the shared Close glyph marks
// removal only.
const (
	keyValueNameHeader  = "Name"
	keyValueValueHeader = "Value"
	keyValueAddLabel    = "Add header"
	keyValueNameHint    = "name"
	keyValueValueHint   = "value"
)

// KeyValuePair is one editable row of a KeyValueGrid.
type KeyValuePair struct {
	Key   string
	Value string
}

// KeyValueGrid is the editable two-column table behind keyed lists such as
// request headers. It replaces a raw text area: every row is one Name/value
// pair, the row set is edited in place (no syntax to remember), and the row
// states reuse the shared list/table language (focused accent, selected,
// hovered) so it reads like every other table in the app.
//
// Keyboard model — arrows move between rows and cells, Enter advances to the
// next cell in reading order (Name → Value → next row), Backspace/Delete
// removes the focused row, and typing on a row starts a fresh edit of that
// cell. The add row below the last pair creates the next row.
type KeyValueGrid struct {
	addLabel                  string
	nameCaption, valueCaption string
	Base
	tokens   *TokenSet
	label    string
	detail   string
	rows     []KeyValuePair
	row      int // focused row index; len(rows) is the add row
	col      int // 0 = name column, 1 = value column
	editing  bool
	readOnly bool
	hover    int
	scroll   *ScrollModel
	keyEdit  *TextField
	valEdit  *TextField
	changed  func()
}

// NewKeyValueGrid builds an empty grid. Rows arrive through SetValue/SetRows.
// SetCaptions names the two columns ("Variable", "Value").
func (g *KeyValueGrid) SetCaptions(name, value string) { g.nameCaption, g.valueCaption = name, value }

func (g *KeyValueGrid) captions() (string, string) {
	name, value := keyValueNameHeader, keyValueValueHeader
	if g.nameCaption != "" {
		name = g.nameCaption
	}
	if g.valueCaption != "" {
		value = g.valueCaption
	}
	return name, value
}

// SetAddLabel names the append row for what the table holds ("Add variable").
func (g *KeyValueGrid) SetAddLabel(label string) { g.addLabel = label }

func NewKeyValueGrid(id ComponentID, tokens *TokenSet) *KeyValueGrid {
	return &KeyValueGrid{Base: NewBase(id), tokens: tokens, hover: -1, scroll: NewScrollModel()}
}

func (g *KeyValueGrid) SetTokens(ts *TokenSet) {
	g.tokens = ts
	if g.keyEdit != nil {
		g.keyEdit.SetTokens(ts)
	}
	if g.valEdit != nil {
		g.valEdit.SetTokens(ts)
	}
}

// SetTitle carries the owning field's label and description so the grid can
// answer Semantics without the parent re-publishing them.
func (g *KeyValueGrid) SetTitle(label, detail string) { g.label, g.detail = label, detail }

// SetReadOnly renders the inherited set without editing affordances. The
// parent decides this from the field state (disabled/read-only).
func (g *KeyValueGrid) SetReadOnly(v bool) {
	if v {
		g.editing = false
	}
	g.readOnly = v
}

func (g *KeyValueGrid) ReadOnly() bool { return g.readOnly }

// OnChanged fires after any row mutation so hosts can re-read the value.
func (g *KeyValueGrid) OnChanged(fn func()) { g.changed = fn }

// SetRows replaces the row set, keeping the focused row when it still exists.
func (g *KeyValueGrid) SetRows(rows []KeyValuePair) {
	next := make([]KeyValuePair, len(rows))
	copy(next, rows)
	g.rows = next
	g.row = g.clampRow(g.row)
	if g.row >= len(g.rows) {
		g.editing = false
	}
	if g.scroll != nil {
		g.scroll.SetContent(len(g.rows))
		g.scroll.EnsureVisible(g.row)
	}
}

// Rows returns a copy of the current row set in display order.
func (g *KeyValueGrid) Rows() []KeyValuePair {
	out := make([]KeyValuePair, len(g.rows))
	copy(out, g.rows)
	return out
}

// maxRow is the focusable row ceiling: the add row exists only when the set is
// editable, so an inherited table can never park focus on a dead affordance.
func (g *KeyValueGrid) maxRow() int {
	if g.readOnly {
		return max(0, len(g.rows)-1)
	}
	return len(g.rows)
}

// clampRow keeps a focus row inside the live rows and the add row.
func (g *KeyValueGrid) clampRow(row int) int {
	return min(max(row, 0), g.maxRow())
}

// Count reports the live row count (the add row is not a row).
func (g *KeyValueGrid) Count() int { return len(g.rows) }

// FocusedRow reports the focused row index; len(rows) is the add row.
func (g *KeyValueGrid) FocusedRow() int { return g.row }

// Editing reports whether a cell editor is open.
func (g *KeyValueGrid) Editing() bool { return g.editing }

// SetValue loads rows from the shared "Name: value" line protocol. A line that
// is not a pair is preserved as a row with an empty value so the mistake stays
// visible and fixable in the table instead of silently vanishing.
func (g *KeyValueGrid) SetValue(text string) { g.SetRows(ParseKeyValueLines(text)) }

// Value serializes the rows back to the "Name: value" line protocol, in the
// order the user arranged them.
func (g *KeyValueGrid) Value() string { return FormatKeyValueLines(g.rows) }

// ActiveEditor returns the cell editor currently open, or nil. The parent uses
// it for clipboard targets and cursor-frame ticking.
func (g *KeyValueGrid) ActiveEditor() *TextField {
	if !g.editing || g.row >= len(g.rows) {
		return nil
	}
	return g.editor(g.col)
}

// NeedsFrame reports cursor blink ownership while a cell editor is open.
func (g *KeyValueGrid) NeedsFrame() bool { return g.editing }

// PreferredHeight reports the grid rows for the current row set: the column
// header, the visible rows, and the add row.
func (g *KeyValueGrid) PreferredHeight() int {
	return 1 + min(len(g.rows), maxKeyValueRows) + g.addRows()
}

// addRows is 1 while the set is editable (the add row), 0 while inherited.
func (g *KeyValueGrid) addRows() int {
	if g.readOnly {
		return 0
	}
	return 1
}

func (g *KeyValueGrid) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: g.PreferredHeight()})
}

func (g *KeyValueGrid) Layout(_ LayoutContext, r Rect) {
	g.SetBounds(r)
	if g.scroll == nil {
		g.scroll = NewScrollModel()
	}
	g.scroll.SetContent(len(g.rows))
	g.scroll.SetViewport(max(1, r.H-1-g.addRows()))
	g.scroll.EnsureVisible(g.row)
	if !g.editing || g.row >= len(g.rows) {
		return
	}
	rowY := 1 + (g.row - g.scroll.Offset())
	if rowY < 1 || rowY >= max(1, r.H-g.addRows()) {
		return
	}
	keyW := g.nameWidth(r.W)
	cellX, cellW := keyW+1, max(1, r.W-keyW-keyValueGutter)
	if g.col == 0 {
		cellX, cellW = 1, max(1, keyW-1)
	}
	g.editor(g.col).Layout(LayoutContext{}, Rect{X: cellX, Y: rowY, W: cellW, H: 1})
}

// nameWidth resolves the name column: wide enough for the longest live name,
// capped at half the grid so the value column never collapses.
func (g *KeyValueGrid) nameWidth(w int) int {
	nameCaption, _ := g.captions()
	width := rendering.VisibleWidth(nameCaption) + 1
	for _, row := range g.rows {
		width = max(width, rendering.VisibleWidth(row.Key)+1)
	}
	if half := (w - keyValueGutter) / 2; half > 0 && width > half {
		width = half
	}
	return max(1, width)
}

// rowAt maps a grid-local y to a row index, or -1 outside the row band.
func (g *KeyValueGrid) rowAt(localY int) int {
	r := g.Bounds()
	if localY < 1 || localY >= r.H {
		return -1
	}
	if localY == r.H-1 && !g.readOnly {
		return len(g.rows) // the add row
	}
	idx := g.scroll.Offset() + (localY - 1)
	if idx >= len(g.rows) {
		return -1
	}
	return idx
}

func (g *KeyValueGrid) color(role SemanticColor) cell.Color {
	if g.tokens == nil {
		return cell.NoColor
	}
	return g.tokens.Color(role)
}

func (g *KeyValueGrid) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := g.Bounds()
	if r.Empty() {
		return
	}
	x, y := originX+r.X, originY+r.Y
	// The panel fill mirrors the form field: the raised subtle fill while the
	// field is idle, the row highlight once it owns focus.
	panel := g.color(ColorPopupSubtleBg)
	if ctx.Focused && !g.readOnly {
		panel = g.color(ColorHover)
	}
	FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: r.H}, cell.BlankCell(panel))
	g.paintHeaderRow(buf, x, y, r.W)
	off, vh := g.scroll.Offset(), g.scroll.Viewport()
	for row := 0; row < vh && off+row < len(g.rows); row++ {
		g.paintRow(buf, x, y, y+1+row, r.W, off+row, ctx)
	}
	if g.addRows() > 0 {
		g.paintAddRow(buf, x, y+r.H-1, r.W, ctx)
	}
}

func (g *KeyValueGrid) paintHeaderRow(buf *surface.CellBuffer, x, y, w int) {
	headBG := g.color(ColorPopupBg)
	headFG := g.color(ColorMuted)
	FillRect(buf, 0, 0, Rect{X: x, Y: y, W: w, H: 1}, cell.BlankCell(headBG))
	nameCaption, valueCaption := g.captions()
	buf.WriteString(x+1, y, rendering.TruncateVisual(nameCaption, max(1, w-2), ""), headFG, headBG, cell.AttrBold)
	if x0 := g.nameWidth(w) + 1; x0 < w-keyValueGutter {
		buf.WriteString(x+x0, y, rendering.TruncateVisual(valueCaption, max(1, w-x0-1), ""), headFG, headBG, cell.AttrBold)
	}
}

// rowState resolves one row's list state: keyboard focus wins over the
// pointer's hover, exactly as ui.Table and ui.List resolve theirs.
func (g *KeyValueGrid) rowState(idx int, ctx PaintContext) ControlState {
	switch {
	case idx == g.row:
		if ctx.Focused && !g.readOnly {
			return StateFocused
		}
		return StateSelected
	case idx == g.hover && !g.readOnly:
		return StateHovered
	default:
		return StateDefault
	}
}

// paintRow paints one row. gridY is the grid's own paint origin (the cell
// editor adds its own bounds to it); rowY is this row's absolute y.
func (g *KeyValueGrid) paintRow(buf *surface.CellBuffer, x, gridY, rowY, w, idx int, ctx PaintContext) {
	st := g.rowState(idx, ctx)
	fg, bg := cell.NoColor, cell.NoColor
	if g.tokens != nil {
		fg, bg = g.tokens.ListRowColors(st)
	}
	if st != StateDefault && !bg.IsDefault() {
		// Idle rows keep the panel fill so the raised table reads as one
		// surface; only focused, selected, and hovered rows take a list tint.
		FillRect(buf, 0, 0, Rect{X: x, Y: rowY, W: w, H: 1}, cell.BlankCell(bg))
	} else {
		bg = g.color(ColorPopupSubtleBg)
		if ctx.Focused && !g.readOnly {
			bg = g.color(ColorHover)
		}
	}
	primary, muted := fg, fg
	if g.tokens != nil {
		primary, muted = g.tokens.MenuInk(st, fg)
	}
	if g.readOnly {
		primary, muted = g.color(ColorMuted), g.color(ColorMuted)
	}
	nameW := g.nameWidth(w)
	g.paintCell(buf, x, gridY, rowY, w, idx, 0, 1, max(1, nameW-1), primary, muted, bg, ctx)
	g.paintCell(buf, x, gridY, rowY, w, idx, 1, nameW+1, max(1, w-nameW-keyValueGutter), primary, muted, bg, ctx)
	if !g.readOnly {
		g.paintRemove(buf, x, rowY, w, st, primary, muted, bg)
	}
}

func (g *KeyValueGrid) paintCell(buf *surface.CellBuffer, x, gridY, rowY, w, idx, col, cellX, cellW int, primary, muted, bg cell.Color, ctx PaintContext) {
	row := g.rows[idx]
	text, hint := row.Key, keyValueNameHint
	if col == 1 {
		text, hint = row.Value, keyValueValueHint
	}
	if g.editing && ctx.Focused && idx == g.row && g.col == col {
		// The editor carries its own grid-relative bounds: hand it the grid's
		// paint origin, never this row's y. An unfocused grid paints no caret.
		ed := g.editor(col)
		ed.SetBackgroundColor(bg)
		ed.Paint(buf, x, gridY, PaintContext{Theme: ctx.Theme, Focused: true, Hovered: false, Frame: ctx.Frame})
		return
	}
	if strings.TrimSpace(text) == "" {
		buf.WriteString(x+cellX, rowY, rendering.TruncateVisual(hint, cellW, ""), muted, bg, cell.AttrItalic)
		return
	}
	attr := cell.Attrs(0)
	if col == 0 && idx == g.row {
		attr = cell.AttrBold
	}
	buf.WriteString(x+cellX, rowY, rendering.TruncateVisual(text, cellW, ""), primary, bg, attr)
}

// paintRemove draws the shared Close glyph in the right gutter of a row.
func (g *KeyValueGrid) paintRemove(buf *surface.CellBuffer, x, y, w int, st ControlState, primary, muted, bg cell.Color) {
	glyph := string(FirstRune(DefaultGlyphsFromTokens(g.tokens).Close, '✕'))
	fg := muted
	switch st {
	case StateFocused, StateSelected:
		fg = primary
	case StateHovered:
		fg = g.color(ColorError)
	}
	buf.WriteString(x+w-2, y, glyph, fg, bg, 0)
}

func (g *KeyValueGrid) paintAddRow(buf *surface.CellBuffer, x, y, w int, ctx PaintContext) {
	st := g.rowState(len(g.rows), ctx)
	fg, bg := cell.NoColor, cell.NoColor
	if g.tokens != nil {
		fg, bg = g.tokens.ListRowColors(st)
	}
	if st != StateDefault && !bg.IsDefault() {
		FillRect(buf, 0, 0, Rect{X: x, Y: y, W: w, H: 1}, cell.BlankCell(bg))
	} else {
		bg = g.color(ColorPopupSubtleBg)
		if ctx.Focused && !g.readOnly {
			bg = g.color(ColorHover)
		}
	}
	primary := fg
	if g.tokens != nil {
		primary, _ = g.tokens.MenuInk(st, fg)
	}
	attr := cell.Attrs(0)
	if st == StateFocused || st == StateSelected {
		attr = cell.AttrBold
	}
	label := keyValueAddLabel
	if g.addLabel != "" {
		label = g.addLabel
	}
	buf.WriteString(x+1, y, rendering.TruncateVisual(label, max(1, w-2), ""), primary, bg, attr)
}

// editor returns the retained editor for a column, creating it on first use.
func (g *KeyValueGrid) editor(col int) *TextField {
	if col == 0 {
		if g.keyEdit == nil {
			g.keyEdit = NewTextField(NewID(g.ID(), "name"), g.tokens)
			g.keyEdit.SetPlaceholder(keyValueNameHint)
		}
		return g.keyEdit
	}
	if g.valEdit == nil {
		g.valEdit = NewTextField(NewID(g.ID(), "value"), g.tokens)
		g.valEdit.SetPlaceholder(keyValueValueHint)
	}
	return g.valEdit
}

func (g *KeyValueGrid) Handle(ctx EventContext, evt Event) EventResult {
	r := g.Bounds()
	if r.Empty() {
		return EventResult{}
	}
	switch evt.Kind {
	case EventKey:
		return g.handleKey(ctx, evt)
	case EventMouseMotion:
		if !r.Contains(ctx.LocalX, ctx.LocalY) {
			return EventResult{}
		}
		if idx := g.rowAt(ctx.LocalY); idx != g.hover {
			g.hover = idx
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
		return EventResult{Handled: true}
	case EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft || !r.Contains(ctx.LocalX, ctx.LocalY) {
			return EventResult{}
		}
		return g.handleClick(ctx.LocalX, ctx.LocalY)
	case EventMouseWheel:
		if g.scroll == nil || g.scroll.MaxOffset() <= 0 {
			return EventResult{}
		}
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			g.scroll.ScrollBy(-3)
		case tea.MouseWheelDown:
			g.scroll.ScrollBy(3)
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case EventCustom:
		if !g.editing {
			return EventResult{}
		}
		paste, ok := evt.Custom.(tea.PasteMsg)
		if !ok {
			return EventResult{}
		}
		ed := g.ActiveEditor()
		if ed == nil {
			return EventResult{}
		}
		ed.PasteText(paste.Content)
		g.closeCell()
		return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
	}
	return EventResult{}
}

func (g *KeyValueGrid) handleKey(ctx EventContext, evt Event) EventResult {
	if g.readOnly {
		return EventResult{}
	}
	if g.editing {
		if res, ok := g.handleEditingKey(evt); ok {
			return res
		}
		// Everything the cell editor owns (runes, Home/End, word and line
		// edits, clipboard chords) goes straight to it.
		return g.editKey(ctx, evt)
	}
	switch evt.Key.Code {
	case tea.KeyUp:
		g.moveRow(-1)
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case tea.KeyDown:
		g.moveRow(1)
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case tea.KeyPgUp:
		g.pageRows(-1)
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case tea.KeyPgDown:
		g.pageRows(1)
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case tea.KeyLeft:
		if g.col == 1 {
			g.col = 0
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case tea.KeyRight:
		if g.col == 0 && g.row < len(g.rows) {
			g.col = 1
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case tea.KeyEnter:
		return g.activate()
	case tea.KeyBackspace, tea.KeyDelete:
		if !g.editing && g.row < len(g.rows) {
			g.removeRow(g.row)
			return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
		}
		return EventResult{}
	default:
		if g.row >= len(g.rows) || evt.Key.Text == "" {
			return EventResult{}
		}
		// Typing on a row starts a fresh edit of that cell: the first rune
		// replaces the old text, like every table editor worth using.
		g.startEdit(g.row, g.col, true)
		return g.editKey(ctx, evt)
	}
}

// editKey forwards a key to the open cell editor and mirrors the result back
// into the row, so the caret keeps blinking while the user types.
func (g *KeyValueGrid) editKey(ctx EventContext, evt Event) EventResult {
	ed := g.ActiveEditor()
	if ed == nil {
		return EventResult{}
	}
	res := ed.Handle(ChildEventContext(ctx, ed.Bounds()), evt)
	g.writeCell()
	if !res.Handled {
		return res
	}
	return EventResult{Handled: true, Invalidate: InvalidatePaint}
}

// handleEditingKey answers the keys a cell editor must not swallow. It
// reports false for everything the TextField owns (runes, Home/End, word and
// line edits, clipboard chords).
func (g *KeyValueGrid) handleEditingKey(evt Event) (EventResult, bool) {
	switch evt.Key.Code {
	case tea.KeyEnter:
		g.closeCell()
		g.advance()
		return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}, true
	case tea.KeyUp:
		g.closeCell()
		g.moveRow(-1)
		g.openCell()
		return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}, true
	case tea.KeyDown:
		g.closeCell()
		g.moveRow(1)
		g.openCell()
		return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}, true
	case tea.KeyEsc:
		g.editing = false
		return EventResult{Handled: true, Invalidate: InvalidatePaint}, true
	}
	return EventResult{}, false
}

// activate drives the row under focus: the add row appends a pair and opens
// its name cell; a data row opens the focused cell.
func (g *KeyValueGrid) activate() EventResult {
	if g.row >= len(g.rows) {
		g.rows = append(g.rows, KeyValuePair{})
		g.row = len(g.rows) - 1
		g.col = 0
		g.editing = false
		g.startEdit(g.row, 0, true)
		g.emit()
		return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
	}
	g.openCell()
	return EventResult{Handled: true, Invalidate: InvalidatePaint}
}

func (g *KeyValueGrid) handleClick(localX, localY int) EventResult {
	w := g.Bounds().W
	idx := g.rowAt(localY)
	if idx < 0 {
		return EventResult{}
	}
	if g.readOnly {
		g.row = idx
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	}
	if idx < len(g.rows) && localX >= w-2 {
		g.removeRow(idx)
		return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
	}
	g.row, g.editing = idx, false
	if idx >= len(g.rows) {
		return g.activate()
	}
	if localX >= g.nameWidth(w)+1 {
		g.col = 1
	} else {
		g.col = 0
	}
	g.openCell()
	return EventResult{Handled: true, Invalidate: InvalidatePaint}
}

// openCell opens the focused cell for editing without disturbing its text.
func (g *KeyValueGrid) openCell() {
	if g.readOnly || g.row >= len(g.rows) {
		g.editing = false
		return
	}
	g.startEdit(g.row, g.col, false)
}

// startEdit opens a cell, optionally clearing it so the first rune replaces
// the previous content.
func (g *KeyValueGrid) startEdit(row, col int, replace bool) {
	if g.readOnly || row >= len(g.rows) {
		return
	}
	g.row, g.col, g.editing = row, col, true
	ed := g.editor(col)
	text := g.rows[row].Key
	if col == 1 {
		text = g.rows[row].Value
	}
	if replace {
		text = ""
	}
	ed.SetValue(text)
	ed.Edit().SetCursor(ed.Edit().Len(), false)
}

// writeCell copies the open editor into its row and leaves the editor open so
// the caret survives the keystroke.
func (g *KeyValueGrid) writeCell() {
	if !g.editing || g.row >= len(g.rows) {
		return
	}
	text := g.editor(g.col).Value()
	if g.col == 0 {
		g.rows[g.row].Key = text
	} else {
		g.rows[g.row].Value = text
	}
	g.emit()
}

// closeCell writes the open editor back and leaves edit mode.
func (g *KeyValueGrid) closeCell() {
	if !g.editing {
		return
	}
	g.writeCell()
	g.editing = false
}

// BlurCell ends an in-progress cell edit without losing the typed text, so a
// field that lost focus never keeps a caret or holds unwritten rows.
func (g *KeyValueGrid) BlurCell() {
	g.closeCell()
	g.hover = -1
}

// advance steps to the next cell in reading order: Value on the same row, then
// the Name cell of the next row, then the add row.
func (g *KeyValueGrid) advance() {
	if g.col == 0 && g.row < len(g.rows) {
		g.col = 1
		g.startEdit(g.row, 1, false)
		return
	}
	g.row = g.clampRow(g.row + 1)
	g.col = 0
	g.editing = false
	if g.row < len(g.rows) {
		g.startEdit(g.row, 0, false)
	}
}

// pageRows moves the focus by a viewport, matching ui.Table's paging.
func (g *KeyValueGrid) pageRows(delta int) {
	step := max(1, g.scroll.Viewport()-1)
	g.moveRow(delta * step)
}

func (g *KeyValueGrid) moveRow(delta int) {
	next := g.clampRow(g.row + delta)
	if next == g.row {
		return
	}
	g.closeCell()
	g.row = next
	if g.scroll != nil {
		g.scroll.EnsureVisible(next)
	}
}

// removeRow drops a row and keeps focus on a neighbour.
func (g *KeyValueGrid) removeRow(idx int) {
	if idx < 0 || idx >= len(g.rows) {
		return
	}
	g.rows = append(g.rows[:idx:idx], g.rows[idx+1:]...)
	g.editing = false
	g.row = g.clampRow(g.row)
	if g.scroll != nil {
		g.scroll.SetContent(len(g.rows))
		g.scroll.EnsureVisible(g.row)
	}
	g.emit()
}

func (g *KeyValueGrid) emit() {
	if g.changed != nil {
		g.changed()
	}
}

func (g *KeyValueGrid) Semantics() Semantics {
	return Semantics{Role: RoleList, Label: g.label, Description: g.detail, Value: g.Value()}
}

// ParseKeyValueLines splits the "Name: value" line protocol into rows. A line
// without a colon becomes a row with an empty value so the mistake stays
// visible and fixable in the table.
func ParseKeyValueLines(text string) []KeyValuePair {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var rows []KeyValuePair
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		key, value, _ := strings.Cut(line, ":")
		rows = append(rows, KeyValuePair{Key: strings.TrimSpace(key), Value: strings.TrimSpace(value)})
	}
	return rows
}

// FormatKeyValueLines renders rows back to the "Name: value" line protocol.
// Rows that carry neither a name nor a value are dropped, so an abandoned
// empty row never reaches the stored header set.
func FormatKeyValueLines(rows []KeyValuePair) string {
	if len(rows) == 0 {
		return ""
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		key, value := strings.TrimSpace(row.Key), strings.TrimSpace(row.Value)
		if key == "" {
			continue
		}
		if value == "" {
			lines = append(lines, key)
			continue
		}
		lines = append(lines, key+": "+value)
	}
	return strings.Join(lines, "\n")
}
