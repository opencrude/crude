package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// FormField renders and edits one FieldState. Its value is committed through
// FormModel so dirty state, validation, and screen-level change handling share
// one source of truth.
type FormField struct {
	Base
	model   *FormModel
	field   *FieldState
	tokens  *TokenSet
	control *TextField
	area    *TextArea
	grid    *KeyValueGrid
	labelW  int

	selectOpen      bool
	selectList      *List
	selectFilter    *FilterField
	selectQuery     string
	overlayY        int
	surfaceRole     SemanticColor
	surfaceSet      bool
	compact         bool
	hideDescription bool
	colWidths       []int
}

// Spacious control geometry. Every control is a box that starts on the form's
// value column; text inside a box starts one cell in. Inputs, dropdowns,
// steppers, drill-ins, tables and text areas share one width, so their right
// edges line up too; it is capped at a comfortable reading measure.
const (
	wideControlW  = 60
	shortControlW = 12
)

const selectOverlayChrome = 1

func NewFormField(id ComponentID, f *FieldState, tokens *TokenSet) *FormField {
	return NewBoundFormField(id, nil, f, tokens)
}

// NewBoundFormField binds a control to the supplied form model.
func NewBoundFormField(id ComponentID, model *FormModel, f *FieldState, tokens *TokenSet) *FormField {
	ff := &FormField{Base: NewBase(id), model: model, field: f, tokens: tokens}
	// Read-only hex colour reflections paint as swatches — no text editor.
	if (f.Type == FieldText || f.Type == FieldPassword) && (!f.ReadOnly || !isHexColor(f.Value.String())) {
		ff.control = NewTextField(NewID(id, "input"), tokens)
		ff.control.SetValue(f.Value.String())
		ff.control.SetPlaceholder(f.Placeholder)
		ff.control.SetPassword(f.Type == FieldPassword)
		ff.control.SetReadOnly(f.ReadOnly)
		ff.control.SetDisabled(f.Disabled)
		submitAction := f.SubmitAction
		if submitAction == "" {
			submitAction = "form.next"
		}
		ff.control.SetSubmitAction(submitAction)
	}
	if f.Type == FieldTextArea {
		ff.area = NewTextArea(NewID(id, "input"), tokens, true)
		ff.area.SetValue(f.Value.String())
		ff.area.SetPlaceholder(f.Placeholder)
		ff.area.SetReadOnly(f.ReadOnly)
	}
	if f.Type == FieldKeyValues {
		ff.grid = NewKeyValueGrid(NewID(id, "grid"), tokens)
		ff.grid.SetTitle(f.Label, f.Description)
		ff.grid.SetValue(f.Value.String())
		ff.grid.SetReadOnly(f.ReadOnly || f.Disabled)
		// A key/value field's placeholder names its append row.
		ff.grid.SetAddLabel(f.Placeholder)
		// Two Columns name the table's columns.
		if len(f.Columns) == 2 {
			ff.grid.SetCaptions(f.Columns[0], f.Columns[1])
		}
	}
	if f.Type == FieldSelect || f.Type == FieldMultiSelect {
		ff.selectList = NewList(NewID(id, "options"), tokens)
		ff.selectList.SetActivateAction("form.select.commit")
		// Raised card: sub-pane fill so the overlay reads apart from the form.
		ff.selectList.SetDefaultRowBackground(ColorSubPaneBg)
		ff.selectFilter = NewFilterField(NewID(id, "filter"), tokens)
		ff.selectFilter.SetPrompt(SearchGlyphEnd(tokens))
		ff.selectFilter.SetPromptPlacement(PromptEnd)
		ff.selectFilter.SetPlaceholder("Filter…")
		ff.selectFilter.SetSubmitAction("form.select.commit")
		ff.selectFilter.OnChanged(func(q string) { ff.applySelectFilter(q) })
		ff.syncSelectItems(false)
	}
	return ff
}

func (ff *FormField) SetSurfaceBackground(role SemanticColor) {
	ff.surfaceRole = role
	ff.surfaceSet = true
}

func (ff *FormField) SetCompact(v bool) { ff.compact = v }

func (ff *FormField) SetHideDescription(v bool) { ff.hideDescription = v }

func (ff *FormField) SetColumnWidths(w []int) { ff.colWidths = w }

func (ff *FormField) surfaceBg() cell.Color {
	if ff.tokens == nil {
		return cell.NoColor
	}
	if ff.surfaceSet {
		return ff.tokens.Color(ff.surfaceRole)
	}
	return ff.tokens.Color(ColorPopupBg)
}

func (ff *FormField) SetTokens(ts *TokenSet) {
	ff.tokens = ts
	if ff.control != nil {
		ff.control.SetTokens(ts)
	}
	if ff.area != nil {
		ff.area.SetTokens(ts)
	}
	if ff.grid != nil {
		ff.grid.SetTokens(ts)
	}
	if ff.selectList != nil {
		ff.selectList.SetTokens(ts)
	}
	if ff.selectFilter != nil {
		ff.selectFilter.SetTokens(ts)
	}
}

func (ff *FormField) FieldState() *FieldState { return ff.field }
func (ff *FormField) SetLabelWidth(w int)     { ff.labelW = w }
func (ff *FormField) SelectOpen() bool        { return ff.selectOpen }

// Control returns the retained text control for text/password fields, or nil.
func (ff *FormField) Control() *TextField {
	if ff.control == nil {
		return nil
	}
	return ff.control
}

// Area returns the retained multi-line editor for FieldTextArea fields, or nil.
func (ff *FormField) Area() *TextArea { return ff.area }

// Grid returns the retained key/value table for FieldKeyValues fields, or nil.
func (ff *FormField) Grid() *KeyValueGrid { return ff.grid }

// TextEditor returns the editor this field owns so context menus can copy or
// cut the field's text (TextEditorProvider). Nil for non-text fields.
func (ff *FormField) TextEditor() TextClipboardTarget {
	if ff.grid != nil {
		return ff.grid.ActiveEditor()
	}
	if ff.area != nil {
		return ff.area
	}
	if ff.control != nil {
		return ff.control
	}
	return nil
}

// MultiLine reports whether the field owns a multi-row editor, so form-level
// Up/Down focus movement must reach the caret inside it first.
func (ff *FormField) MultiLine() bool {
	if ff.field == nil {
		return false
	}
	return ff.field.Type == FieldTextArea || ff.field.Type == FieldKeyValues
}

// areaRows returns the visible editor rows for the multi-line field.
func (ff *FormField) areaRows() int {
	if ff.field.Rows > 0 {
		return ff.field.Rows
	}
	return 4
}

// Sync reflects programmatic FormModel changes without replacing the retained
// editor or losing its selection/cursor when the value is unchanged.
func (ff *FormField) Sync() {
	if ff.control != nil && ff.control.Value() != ff.field.Value.String() {
		ff.control.SetValue(ff.field.Value.String())
	}
	if ff.control != nil {
		ff.control.SetPlaceholder(ff.field.Placeholder)
		ff.control.SetReadOnly(ff.field.ReadOnly)
		ff.control.SetDisabled(ff.field.Disabled)
	}
	if ff.area != nil && ff.area.Value() != ff.field.Value.String() {
		ff.area.SetValue(ff.field.Value.String())
	}
	if ff.area != nil {
		ff.area.SetPlaceholder(ff.field.Placeholder)
		ff.area.SetReadOnly(ff.field.ReadOnly)
	}
	if ff.grid != nil {
		if ff.grid.Value() != ff.field.Value.String() {
			ff.grid.SetValue(ff.field.Value.String())
		}
		ff.grid.SetTitle(ff.field.Label, ff.field.Description)
		ff.grid.SetReadOnly(ff.field.ReadOnly || ff.field.Disabled)
	}
	ff.syncSelectItems(ff.selectOpen)
}

// keyValuesHeight reports the rows the key/value table occupies below the
// label: the column header, the live rows (capped), and the add row when the
// set is editable. An inherited table drops the add row so the panel ends with
// the last inherited pair.
func (ff *FormField) keyValuesHeight() int {
	rows := len(ff.grid.Rows())
	if ff.compact {
		rows = min(rows, compactKeyValueRows)
	} else {
		rows = min(rows, maxKeyValueRows)
	}
	height := 1 + rows
	if !ff.grid.ReadOnly() {
		height++
	}
	return height
}

// Blur ends any in-progress cell edit so a field that lost focus never keeps a
// caret blinking or holds text the row has not absorbed yet.
func (ff *FormField) Blur() {
	if ff.grid != nil {
		ff.grid.BlurCell()
	}
}

// Children is intentionally empty. FormField retains its input/list controls
// so the enclosing FormBody owns one form focus scope and action route.
func (ff *FormField) Children() []Component { return nil }

func (ff *FormField) CancelPointerCapture() {
	if ff.control != nil {
		ff.control.CancelPointerCapture()
	}
	if ff.area != nil {
		ff.area.CancelPointerCapture()
	}
}

func (ff *FormField) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: ff.height()})
}

func (ff *FormField) height() int {
	if ff.field.Type == FieldSeparator || ff.field.Type == FieldSpacer {
		return 1
	}
	if ff.field.Type == FieldKeyValues {
		h := 1 + ff.keyValuesHeight()
		if !ff.compact {
			// Spacious tables start on the label row, in the value column.
			h = ff.keyValuesHeight()
		}
		if !ff.compact && (!ff.hideDescription || ff.field.DescriptionImportant) && ff.field.Description != "" {
			h++
		}
		return h + len(ff.field.Errors)
	}
	if ff.compact {
		return 1 + len(ff.field.Errors)
	}
	if ff.field.Type == FieldTextArea {
		h := ff.areaRows()
		if ff.field.Description != "" && (!ff.hideDescription || ff.field.DescriptionImportant) {
			h++
		}
		return h + len(ff.field.Errors)
	}
	if ff.field.Type == FieldNav {
		if strings.TrimSpace(ff.navDetail()) != "" {
			return 2
		}
		return 1
	}
	h := 1
	if ff.field.Type == FieldTextArea {
		h += ff.areaRows()
	}
	if ff.field.Description != "" && (!ff.hideDescription || ff.field.DescriptionImportant) {
		h++
	}
	h += len(ff.field.Errors)
	return h
}

func (ff *FormField) Layout(ctx LayoutContext, r Rect) {
	ff.SetBounds(r)
	if !ff.compact {
		ff.layoutSpacious(ctx, r)
		return
	}
	controlX, controlW := ff.controlX(r.W), ff.controlWidth(r.W)
	if ff.control != nil {
		if editorX := ff.compactEditorX(r.W); editorX >= 0 {
			// Compact editor rows sit flush at the shared value column:
			// TextField paints its value at its own bounds with no inset,
			// so the first glyph lands on the table's second column.
			ff.control.Layout(ctx, Rect{X: editorX, Y: 0, W: max(1, r.W-editorX), H: 1})
		} else {
			controlW = ff.inputWidth(controlW)
			ff.control.Layout(ctx, Rect{X: controlX + 1, Y: 0, W: max(1, controlW-2), H: 1})
		}
	}
	if ff.area != nil {
		// Multi-line editor sits below the label row and spans the form width.
		indent := ff.labelIndent()
		ff.area.Layout(ctx, Rect{X: indent + 1, Y: 1, W: max(1, r.W-indent-2), H: ff.areaRows()})
	}
	if ff.grid != nil {
		// The key/value table uses the same body geometry as the multi-line
		// editor so both editors share one left edge and width.
		indent := ff.labelIndent()
		ff.grid.Layout(ctx, Rect{X: indent + 1, Y: 1, W: max(1, r.W-indent-2), H: ff.keyValuesHeight()})
	}
}

// layoutSpacious places the retained editors inside their value-column boxes.
func (ff *FormField) layoutSpacious(ctx LayoutContext, r Rect) {
	vx, boxW := ff.valueX(r.W), ff.boxWidth(r.W)
	if ff.control != nil {
		ff.control.Layout(ctx, Rect{X: vx + 1, Y: 0, W: max(1, boxW-2), H: 1})
	}
	if ff.area != nil {
		ff.area.Layout(ctx, Rect{X: vx + 1, Y: 0, W: max(1, boxW-2), H: ff.areaRows()})
	}
	if ff.grid != nil {
		ff.grid.Layout(ctx, Rect{X: vx, Y: 0, W: max(1, boxW), H: ff.keyValuesHeight()})
	}
}

// valueX is the spacious value column: every control's box starts here.
func (ff *FormField) valueX(width int) int { return ff.controlX(width) + 1 }

// boxWidth is the spacious control width by kind: short boxes for numbers
// with units, content-sized switches and segments, and one shared wide box
// for everything else.
func (ff *FormField) boxWidth(width int) int {
	available := max(1, width-ff.valueX(width))
	switch {
	case (ff.field.Unit != "" || ff.field.Numeric) && (ff.field.Type == FieldText || ff.field.Type == FieldPassword):
		return min(available, max(shortControlW, rendering.VisibleWidth(ff.field.Value.String())+4))
	case ff.field.Type == FieldCycle && ff.field.Style == StyleSwitch:
		// A switch is a compact ‹ On › box sized to its widest state.
		return min(available, ff.maxOptionWidth()+6)
	case ff.field.Type == FieldToggle || ff.field.Type == FieldCheckbox:
		return min(available, rendering.VisibleWidth("Off")+6)
	case ff.field.Type == FieldCycle && ff.cycleSegments(available) != nil:
		segments := ff.cycleSegments(available)
		last := segments[len(segments)-1]
		return last.x + last.w
	}
	return min(available, wideControlW)
}

func (ff *FormField) maxOptionWidth() int {
	w := rendering.VisibleWidth(ff.field.Placeholder)
	for _, option := range ff.field.Options {
		w = max(w, rendering.VisibleWidth(option.Label))
	}
	if w == 0 {
		w = rendering.VisibleWidth("Not set")
	}
	return w
}

// isStepper reports whether a spacious cycle paints as the ‹ value › box.
func (ff *FormField) isStepper(width int) bool {
	if ff.field.Type != FieldCycle || ff.field.Style == StyleSwitch {
		return false
	}
	return ff.cycleSegments(max(1, width-ff.valueX(width))) == nil
}

// selectPanel returns the field-relative x and width of the open dropdown.
func (ff *FormField) selectPanel(width int) (int, int) {
	if ff.compact {
		return ff.controlX(width), ff.controlWidth(width)
	}
	vx := ff.valueX(width)
	return vx, max(1, min(width-vx, max(ff.boxWidth(width), 32)))
}

func (ff *FormField) layoutOverlay(ctx LayoutContext, formH int) {
	if ff.selectList == nil || !ff.selectOpen {
		return
	}
	r := ff.Bounds()
	rows := ff.selectPanelHeight()
	ff.overlayY = 1
	if r.Y+1+rows > formH && r.Y-rows >= 0 {
		ff.overlayY = -rows
	}
	controlX, controlW := ff.selectPanel(r.W)
	innerX := controlX + selectOverlayChrome
	innerW := max(1, controlW-2*selectOverlayChrome)
	if ff.selectFilter != nil {
		ff.selectFilter.Layout(ctx, Rect{X: innerX, Y: ff.overlayY + selectOverlayChrome, W: innerW, H: 1})
	}
	listH := ff.selectRows() - 1
	if listH < 1 {
		listH = 1
	}
	ff.selectList.Layout(ctx, Rect{X: innerX, Y: ff.overlayY + selectOverlayChrome + 1, W: innerW, H: listH})
}

func (ff *FormField) selectPanelHeight() int {
	if !ff.selectOpen {
		return 0
	}
	return ff.selectRows() + 2*selectOverlayChrome
}

func (ff *FormField) labelIndent() int {
	// Spacious drill-ins are ordinary rows: their label shares the field
	// indent and their box the value column.
	if ff.compact || (ff.field != nil && (ff.field.Type == FieldSeparator || ff.field.Type == FieldSpacer)) {
		return 0
	}
	if ff.tokens == nil {
		return 1
	}
	return max(0, ff.tokens.FormIndent())
}

// controlX returns the label→control boundary: labels start FormIndent columns
// in from section titles, and a 2-cell gap separates the longest label from
// its control.
func (ff *FormField) controlX(width int) int {
	lw := ff.labelWidth() + ff.labelIndent()
	if width-lw-1 < 10 {
		return min(lw, width)
	}
	return lw + 1
}

// editorControlX resolves where the row's editor control starts. Compact
// single-line rows join the shared compact table: their editor starts at the
// plan's first value cell so the value lines up with the nav/cycle value
// column around it (TableMinGap after the longest label) instead of hugging
// its own label. Spacious forms and every other field type keep controlX.
// compactEditorX returns -1 when the row cannot hold the table (narrow
// dock), falling back to the label-hugging geometry.
func (ff *FormField) editorControlX(width int) int {
	if x := ff.compactEditorX(width); x >= 0 {
		return x
	}
	return ff.controlX(width)
}

func (ff *FormField) controlWidth(width int) int {
	w := width - ff.controlX(width)
	if w < 1 {
		return 1
	}
	return w
}

func (ff *FormField) labelWidth() int {
	if ff.labelW > 0 {
		return ff.labelW
	}
	return 16
}

func (ff *FormField) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := ff.Bounds()
	if r.Empty() {
		return
	}
	bg := cell.NoColor
	labelFG := cell.NoColor
	valueFG := cell.NoColor
	mutedFG := cell.NoColor
	errorFG := cell.NoColor
	dividerFG := cell.NoColor
	if ff.tokens != nil {
		bg = ff.surfaceBg()
		labelFG = ff.tokens.Color(ColorFg)
		valueFG = ff.tokens.Color(ColorFg)
		mutedFG = ff.tokens.Color(ColorMuted)
		errorFG = ff.tokens.Color(ColorError)
		dividerFG = ff.tokens.Color(ColorPopupDivider)
	}
	// Hover tints a row without moving its keyboard focus. Only the focused
	// field gets accent ink and bold; values and meta keep their usual ink.
	focusedRow := ctx.Focused && !ff.field.skipFocus()
	hoveredRow := ctx.Hovered && !ff.field.skipFocus() && !focusedRow
	rowBG := bg
	if focusedRow || hoveredRow {
		rowBG = ff.tokensColor(ColorHover, bg)
	}
	if focusedRow {
		labelFG = ff.tokensColor(ColorAccent, labelFG)
		valueFG = ff.tokensColor(ColorFg, valueFG)
		mutedFG = ff.tokensColor(ColorMuted, mutedFG)
	}
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	x, y := originX+r.X, originY+r.Y
	if ff.field.Type == FieldSpacer {
		return
	}
	if ff.field.Type == FieldSeparator {
		ff.paintSeparator(buf, x, y, r.W, mutedFG, dividerFG, bg)
		return
	}
	if !ff.compact {
		ff.paintSpacious(buf, originX, originY, ctx, focusedRow, hoveredRow, bg)
		return
	}
	// Compact docks: one table row per field on the chat surface.
	if ff.field.Type == FieldNav || ff.field.Type == FieldCycle {
		ff.paintCompactRow(buf, x, y, r.W, ctx, labelFG, valueFG, mutedFG, bg)
		return
	}

	multiline := ff.field.Type == FieldTextArea || ff.field.Type == FieldKeyValues
	if (focusedRow || hoveredRow) && !multiline {
		FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(rowBG))
	}
	labelBG := rowBG
	if multiline {
		labelBG = bg
	}
	labelX := x + ff.labelIndent()
	label := rendering.TruncateVisual(ff.field.Label, max(1, ff.labelWidth()), "")
	// Markers are appended after truncation so they are never clipped; they
	// borrow up to 2 cells from the 2-cell label→control gap (controlX =
	// labelW+1) and can never overlap the control itself.
	if ff.field.Required && ff.field.Label != "" {
		label += " *"
	}
	if ff.field.Dirty && ff.field.Label != "" {
		label += " •"
	}
	if (ff.field.Disabled || ff.field.ReadOnly) && !focusedRow {
		labelFG = mutedFG
	}
	labelAttr := cell.Attrs(0)
	if focusedRow {
		labelAttr = cell.AttrBold
	}
	buf.WriteString(labelX, y, label, labelFG, labelBG, labelAttr)

	if ff.field.Type == FieldTextArea {
		ff.paintTextAreaBody(buf, x, y, r, ctx, valueFG, mutedFG, errorFG, focusedRow, rowBG, bg)
		return
	}
	if ff.field.Type == FieldKeyValues {
		ff.paintKeyValuesBody(buf, x, y, r, ctx, mutedFG, errorFG, focusedRow, rowBG, bg)
		return
	}

	// Compact editor rows align at the shared table column; every other row
	// keeps the label-hugging control with a one-cell inset.
	editorX := ff.compactEditorX(r.W)
	controlX := x + ff.editorControlX(r.W)
	controlW := ff.controlWidth(r.W)
	if editorX >= 0 {
		controlW = max(1, r.W-editorX)
	}
	contentX := controlX + 1
	if editorX >= 0 {
		contentX = controlX
	}
	contentW := max(1, controlW-2)
	// Compact docks keep the chat surface beneath idle rows.
	controlBG := rowBG
	if editorX < 0 && (ff.field.Type == FieldText || ff.field.Type == FieldPassword) {
		controlW = ff.inputWidth(controlW)
	}

	if ff.field.ReadOnly && isHexColor(ff.field.Value.String()) {
		ff.paintColorValue(buf, contentX, y, contentW, valueFG, mutedFG, controlBG)
	} else if ff.field.Type == FieldText || ff.field.Type == FieldPassword {
		if ff.control != nil {
			fieldCtx := ctx
			fieldCtx.Focused = ctx.Focused
			ff.control.SetBackgroundColor(controlBG)
			ff.control.Paint(buf, originX+r.X, originY+r.Y, fieldCtx)
			if ff.field.Unit != "" && editorX < 0 {
				buf.WriteString(controlX+controlW+1, y, ff.field.Unit, mutedFG, rowBG, 0)
			}
		} else {
			text := ff.field.Value.String()
			if text == "" {
				text = "-"
			}
			buf.WriteString(contentX, y, rendering.TruncateVisual(text, contentW, "…"), valueFG, controlBG, 0)
		}
	} else {
		ff.paintChoice(buf, contentX, y, contentW, ctx, valueFG, mutedFG, controlBG)
	}

	line := 1
	for _, err := range ff.field.Errors {
		if line >= r.H {
			break
		}
		buf.WriteString(contentX, y+line, rendering.TruncateVisual(err, contentW, "…"), errorFG, bg, 0)
		line++
	}
}

// paintSpacious renders one settings row. The row itself is never filled:
// focus is an accent edge plus an accent label, and each control is its own
// box on the shared value column, so stacked rows never fuse into one slab
// and every box edge lines up with the boxes above and below it.
func (ff *FormField) paintSpacious(buf *surface.CellBuffer, originX, originY int, ctx PaintContext, focused, hovered bool, bg cell.Color) {
	r := ff.Bounds()
	x, y := originX+r.X, originY+r.Y
	fg, muted, accent := ff.tokensColor(ColorFg, cell.NoColor), ff.tokensColor(ColorMuted, cell.NoColor), ff.tokensColor(ColorAccent, cell.NoColor)
	inactive := ff.field.Disabled || ff.field.ReadOnly

	indent := ff.labelIndent()
	if focused && indent > 0 {
		edge := DefaultGlyphsFromTokens(ff.tokens).FocusEdge
		buf.WriteString(x+indent-1, y, edge, accent, bg, 0)
	}
	labelFG, labelAttr := fg, cell.Attrs(0)
	switch {
	case focused:
		labelFG, labelAttr = accent, cell.AttrBold
	case inactive:
		labelFG = muted
	}
	label := rendering.TruncateVisual(ff.field.Label, max(1, ff.labelWidth()-1), "…")
	if ff.field.Required && ff.field.Label != "" {
		label += " *"
	}
	if ff.field.Dirty && ff.field.Label != "" {
		label += " " + DefaultGlyphsFromTokens(ff.tokens).Modified
	}
	buf.WriteString(x+indent, y, label, labelFG, bg, labelAttr)

	vx, boxW := x+ff.valueX(r.W), ff.boxWidth(r.W)
	box := ff.boxBG(focused, hovered, bg)
	valueFG := fg
	if ff.field.Disabled {
		valueFG = muted
	}
	rows := 1
	switch ff.field.Type {
	case FieldText, FieldPassword:
		if ff.field.ReadOnly && isHexColor(ff.field.Value.String()) {
			ff.paintColorValue(buf, vx+1, y, boxW-1, valueFG, muted, bg)
			break
		}
		if ff.field.ReadOnly {
			box = bg
		}
		FillRect(buf, 0, 0, Rect{X: vx, Y: y, W: boxW, H: 1}, cell.BlankCell(box))
		if ff.control != nil {
			ff.control.SetBackgroundColor(box)
			ff.control.Paint(buf, x, y, ctx)
		}
		if ff.field.Unit != "" {
			buf.WriteString(vx+boxW+1, y, ff.field.Unit, muted, bg, 0)
		}
	case FieldTextArea:
		rows = ff.areaRows()
		if ff.field.ReadOnly {
			box = bg
		}
		FillRect(buf, 0, 0, Rect{X: vx, Y: y, W: boxW, H: rows}, cell.BlankCell(box))
		if ff.area != nil {
			ff.area.SetBackgroundColor(box)
			ff.area.Paint(buf, x, y, ctx)
		}
	case FieldKeyValues:
		rows = ff.keyValuesHeight()
		if ff.grid != nil {
			ff.grid.Paint(buf, x, y, ctx)
		}
	case FieldSelect, FieldMultiSelect:
		ff.paintSelectBox(buf, vx, y, boxW, focused, valueFG, muted, accent, box)
	case FieldCycle:
		ff.paintSpaciousCycle(buf, vx, y, boxW, r.W, focused, hovered, valueFG, muted, accent, box, bg)
	case FieldToggle, FieldCheckbox:
		text := "Off"
		if ff.field.Value.Bool() {
			text = "On"
		}
		arrow := ff.arrowInk(focused, hovered, valueFG, accent, muted)
		ff.paintArrowBox(buf, vx, y, boxW, text, arrow, arrow, valueFG, box)
	case FieldNav:
		ff.paintNavBox(buf, vx, y, boxW, focused, valueFG, muted, accent, box)
	}

	contentX, contentW := vx+1, max(1, r.W-ff.valueX(r.W)-1)
	line := rows
	detail := ff.field.Description
	if ff.hideDescription && !ff.field.DescriptionImportant {
		detail = ""
	}
	if ff.field.Type == FieldNav {
		detail = ff.navDetail()
	}
	if strings.TrimSpace(detail) != "" && line < r.H {
		buf.WriteString(contentX, y+line, rendering.TruncateVisual(detail, contentW, "…"), muted, bg, 0)
		line++
	}
	errorFG := ff.tokensColor(ColorError, cell.NoColor)
	for _, err := range ff.field.Errors {
		if line >= r.H {
			break
		}
		buf.WriteString(contentX, y+line, rendering.TruncateVisual(err, contentW, "…"), errorFG, bg, 0)
		line++
	}
}

// boxBG is the fill of a spacious control: raised while idle, the hover fill
// while the pointer rests on it or it owns focus.
func (ff *FormField) boxBG(focused, hovered bool, bg cell.Color) cell.Color {
	if (focused || hovered) && !ff.field.Disabled {
		return ff.tokensColor(ColorHover, bg)
	}
	return ff.tokensColor(ColorPopupSubtleBg, bg)
}

func (ff *FormField) paintSelectBox(buf *surface.CellBuffer, x, y, w int, focused bool, fg, muted, accent, box cell.Color) {
	FillRect(buf, 0, 0, Rect{X: x, Y: y, W: w, H: 1}, cell.BlankCell(box))
	indicator := "▾"
	if ff.selectOpen {
		indicator = "▴"
	}
	text, ink, attr := "", fg, cell.Attrs(0)
	if ff.field.Type == FieldMultiSelect {
		labels := make([]string, 0, len(ff.field.Value.Strings()))
		for _, id := range ff.field.Value.Strings() {
			labels = append(labels, ff.optionLabel(id))
		}
		text = strings.Join(labels, ", ")
	} else if value := ff.field.Value.String(); value != "" {
		text = ff.optionLabel(value)
	}
	if text == "" {
		text, ink, attr = ff.field.Placeholder, muted, cell.AttrItalic
		if text == "" {
			text = "Not set"
		}
		if ff.field.Loading {
			text = mapNonEmpty(ff.field.LoadMessage, "Loading…")
		}
	}
	buf.WriteString(x+1, y, rendering.TruncateVisual(text, max(1, w-4), "…"), ink, box, attr)
	arrow := muted
	if focused || ff.selectOpen {
		arrow = accent
	}
	if w >= 3 {
		buf.WriteString(x+w-2, y, indicator, arrow, box, 0)
	}
}

func (ff *FormField) optionLabel(id string) string {
	for _, option := range ff.field.Options {
		if option.ID == id {
			return option.Label
		}
	}
	return id
}

func mapNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// paintNavBox renders a drill-in row as a button box: its summary (or, for
// Button navs, its own label) and a trailing ▸, so it reads as clickable.
func (ff *FormField) paintNavBox(buf *surface.CellBuffer, x, y, w int, focused bool, fg, muted, accent, box cell.Color) {
	FillRect(buf, 0, 0, Rect{X: x, Y: y, W: w, H: 1}, cell.BlankCell(box))
	summary := strings.TrimSpace(ff.field.Value.String())
	if len(ff.field.Columns) > 0 {
		summary = strings.Join(ff.field.Columns, "  ")
	}
	if ff.field.Button || summary == "" {
		summary = mapNonEmpty(summary, "Open")
		if ff.field.Button {
			summary = ff.field.Label
		}
	}
	ink := fg
	if !ff.field.Button && len(ff.field.Columns) == 0 {
		ink = muted
	}
	chevron := string(FirstRune(DefaultGlyphsFromTokens(ff.tokens).TreeCollapsed, '▸'))
	buf.WriteString(x+1, y, rendering.TruncateVisual(summary, max(1, w-4), "…"), ink, box, 0)
	arrow := muted
	if focused {
		arrow = accent
	}
	if w >= 3 {
		buf.WriteString(x+w-2, y, chevron, arrow, box, 0)
	}
}

// paintSpaciousCycle draws a switch, a segmented pill, or a fixed-width
// ‹ value › stepper whose arrows hold their columns as values change.
func (ff *FormField) paintSpaciousCycle(buf *surface.CellBuffer, x, y, w, rowW int, focused, hovered bool, fg, muted, accent, box, bg cell.Color) {
	if ff.field.Disabled {
		accent = muted
	}
	switch {
	case ff.field.Style == StyleSwitch:
		// Two states: both arrows always flip it, so neither dims.
		arrow := ff.arrowInk(focused, hovered, fg, accent, muted)
		ff.paintArrowBox(buf, x, y, w, ff.cycleLabel(), arrow, arrow, fg, box)
		return
	case !ff.isStepper(rowW):
		selected := ff.selectedIndex()
		idle := muted
		if focused || hovered {
			idle = fg
		}
		for _, segment := range ff.cycleSegments(max(1, rowW-ff.valueX(rowW))) {
			option := ff.field.Options[segment.index]
			ink, fill, attr := idle, box, cell.Attrs(0)
			if segment.index == selected {
				fill, attr = accent, cell.AttrBold
				ink = cell.ContrastColor(accent)
			}
			if option.Disabled {
				ink = muted
			}
			buf.WriteString(x+segment.x, y, " "+option.Label+" ", ink, fill, attr)
		}
		return
	}
	arrow := ff.arrowInk(focused, hovered, fg, accent, muted)
	// An arrow with nowhere left to go dims: the scale ends there.
	index, last := ff.selectedIndex(), len(ff.field.Options)-1
	leftInk, rightInk := arrow, arrow
	if index <= 0 {
		leftInk = muted
	}
	if index >= last {
		rightInk = muted
	}
	ff.paintArrowBox(buf, x, y, w, ff.cycleLabel(), leftInk, rightInk, fg, box)
}

func (ff *FormField) arrowInk(focused, hovered bool, fg, accent, muted cell.Color) cell.Color {
	switch {
	case ff.field.Disabled:
		return muted
	case focused || hovered:
		return accent
	}
	return fg
}

// paintArrowBox is the one ‹ value › box shared by steppers and switches:
// arrows on fixed columns at both ends, the value centered between them.
func (ff *FormField) paintArrowBox(buf *surface.CellBuffer, x, y, w int, value string, leftInk, rightInk, fg, box cell.Color) {
	FillRect(buf, 0, 0, Rect{X: x, Y: y, W: w, H: 1}, cell.BlankCell(box))
	g := DefaultGlyphsFromTokens(ff.tokens)
	buf.WriteString(x+1, y, g.CycleLeft, leftInk, box, cell.AttrBold)
	if w >= 4 {
		buf.WriteString(x+w-2, y, g.CycleRight, rightInk, box, cell.AttrBold)
	}
	label := rendering.TruncateVisual(value, max(1, w-6), "…")
	buf.WriteString(x+max(3, (w-rendering.VisibleWidth(label))/2), y, label, fg, box, 0)
}

// paintTextAreaBody renders the multi-line editor below the label row plus
// the description and validation errors underneath it.
func (ff *FormField) paintTextAreaBody(buf *surface.CellBuffer, x, y int, r Rect, ctx PaintContext, valueFG, mutedFG, errorFG cell.Color, focusedRow bool, rowBG, bg cell.Color) {
	if ff.area == nil {
		return
	}
	indent := ff.labelIndent()
	editorX := x + indent + 1
	editorW := max(1, r.W-indent-2)
	// Unfocused editors keep the raised field fill; focused editors keep the
	// row highlight so the whole field reads as one surface.
	editorBG := rowBG
	if !focusedRow {
		editorBG = ff.tokensColor(ColorPopupSubtleBg, bg)
	}
	FillRect(buf, 0, 0, Rect{X: editorX - 1, Y: y + 1, W: editorW + 2, H: ff.areaRows()}, cell.BlankCell(editorBG))
	ff.area.SetBackgroundColor(editorBG)
	// TextArea bounds are FormField-local and already carry Y=1. Pass the
	// field's paint origin unchanged; pre-adding one row double-translates the
	// editor fill and overwrites the description row below it.
	ff.area.Paint(buf, x, y, ctx)

	line := 1 + ff.areaRows()
	if (!ff.hideDescription || ff.field.DescriptionImportant) && ff.field.Description != "" && line < r.H {
		buf.WriteString(editorX, y+line, rendering.TruncateVisual(ff.field.Description, editorW, "…"), mutedFG, bg, 0)
		line++
	}
	for _, err := range ff.field.Errors {
		if line >= r.H {
			break
		}
		buf.WriteString(editorX, y+line, rendering.TruncateVisual(err, editorW, "…"), errorFG, bg, 0)
		line++
	}
}

// paintKeyValuesBody renders the key/value table below the label row plus the
// description and validation errors underneath it. It shares the multi-line
// editor's body geometry and surface treatment.
func (ff *FormField) paintKeyValuesBody(buf *surface.CellBuffer, x, y int, r Rect, ctx PaintContext, mutedFG, errorFG cell.Color, focusedRow bool, rowBG, bg cell.Color) {
	if ff.grid == nil {
		return
	}
	indent := ff.labelIndent()
	editorX := x + indent + 1
	editorW := max(1, r.W-indent-2)
	gridH := ff.keyValuesHeight()
	// Unfocused editors keep the raised field fill; the focused editor keeps
	// the row highlight so the whole field reads as one surface.
	editorBG := rowBG
	if !focusedRow {
		editorBG = ff.tokensColor(ColorPopupSubtleBg, bg)
	}
	FillRect(buf, 0, 0, Rect{X: editorX - 1, Y: y + 1, W: editorW + 2, H: gridH}, cell.BlankCell(editorBG))
	// Grid bounds already carry Y=1: pass the field's paint origin unchanged.
	ff.grid.Paint(buf, x, y, ctx)

	line := 1 + gridH
	if !ff.compact && (!ff.hideDescription || ff.field.DescriptionImportant) && ff.field.Description != "" && line < r.H {
		buf.WriteString(editorX, y+line, rendering.TruncateVisual(ff.field.Description, editorW, "…"), mutedFG, bg, 0)
		line++
	}
	for _, err := range ff.field.Errors {
		if line >= r.H {
			break
		}
		buf.WriteString(editorX, y+line, rendering.TruncateVisual(err, editorW, "…"), errorFG, bg, 0)
		line++
	}
}

func (ff *FormField) paintSeparator(buf *surface.CellBuffer, x, y, width int, labelFG, lineFG, bg cell.Color) {
	if width < 1 {
		return
	}
	ruleY := y
	label := strings.TrimSpace(ff.field.Label)
	border := string(FirstRune(DefaultGlyphsFromTokens(ff.tokens).BorderH, '-'))
	if label == "" {
		buf.WriteString(x, ruleY, strings.Repeat(border, width), lineFG, bg, 0)
		return
	}
	labelWidth := rendering.VisibleWidth(label)
	if labelWidth+3 >= width {
		buf.WriteString(x, ruleY, rendering.TruncateVisual(label, width, ""), labelFG, bg, 0)
		return
	}
	buf.WriteString(x, ruleY, label, labelFG, bg, 0)
	lineX := x + labelWidth + 2
	lineW := width - labelWidth - 2
	buf.WriteString(lineX, ruleY, strings.Repeat(border, lineW), lineFG, bg, 0)
}

func (ff *FormField) PaintOverlay(buf *surface.CellBuffer, originX, originY int, ctx PaintContext, form Rect) {
	if !ff.selectOpen || ff.selectList == nil {
		return
	}
	r := ff.Bounds()
	x, y := originX+r.X, originY+r.Y
	panelBG := ff.tokensColor(ColorSubPaneBg, ff.tokensColor(ColorPopupBg, cell.NoColor))
	filterBG := ff.tokensColor(ColorPopupSubtleBg, panelBG)
	mutedFG := ff.tokensColor(ColorMuted, cell.NoColor)
	borderFG := ff.tokensColor(ColorPopupBorder, cell.NoColor)
	if ctx.Focused {
		borderFG = ff.tokensColor(ColorPopupBorderFocused, borderFG)
	}
	panelX, controlW := ff.selectPanel(r.W)
	controlX := x + panelX
	contentX := controlX + selectOverlayChrome
	contentW := max(1, controlW-2*selectOverlayChrome)
	rows := ff.selectPanelHeight()
	panel := Rect{X: controlX, Y: y + ff.overlayY, W: controlW, H: rows}
	FillRect(buf, 0, 0, panel, cell.BlankCell(panelBG))
	ff.paintSelectFrame(buf, panel, borderFG, panelBG)
	if ff.selectFilter != nil {
		filterRow := Rect{X: contentX, Y: y + ff.overlayY + selectOverlayChrome, W: contentW, H: 1}
		FillRect(buf, 0, 0, filterRow, cell.BlankCell(filterBG))
		filterCtx := ctx
		filterCtx.Focused = ctx.Focused
		ff.selectFilter.SetBackgroundColor(filterBG)
		ff.selectFilter.Paint(buf, originX+r.X, originY+r.Y, filterCtx)
	}
	if len(ff.field.Options) == 0 {
		message := "No options available"
		if ff.field.Loading {
			message = ff.field.LoadMessage
			if message == "" {
				message = "Loading..."
			}
		}
		buf.WriteString(contentX, y+ff.overlayY+selectOverlayChrome+1, rendering.TruncateVisual(message, contentW, "…"), mutedFG, panelBG, 0)
		return
	}
	ff.selectList.Paint(buf, originX+r.X, originY+r.Y, ctx)
	_ = form
}

func (ff *FormField) paintSelectFrame(buf *surface.CellBuffer, panel Rect, borderFG, bg cell.Color) {
	if panel.W < 2 || panel.H < 2 {
		return
	}
	g := DefaultGlyphsFromTokens(ff.tokens)
	set := func(px, py int, ch rune) {
		buf.Set(panel.X+px, panel.Y+py, cell.NewCell(ch, borderFG, bg, 0))
	}
	set(0, 0, FirstRune(g.CornerTL, '╭'))
	set(panel.W-1, 0, FirstRune(g.CornerTR, '╮'))
	set(0, panel.H-1, FirstRune(g.CornerBL, '╰'))
	set(panel.W-1, panel.H-1, FirstRune(g.CornerBR, '╯'))
	hBorder := FirstRune(g.BorderH, '─')
	vBorder := FirstRune(g.BorderV, '│')
	for col := 1; col < panel.W-1; col++ {
		set(col, 0, hBorder)
		set(col, panel.H-1, hBorder)
	}
	for row := 1; row < panel.H-1; row++ {
		set(0, row, vBorder)
		set(panel.W-1, row, vBorder)
	}
}

func (ff *FormField) paintChoice(buf *surface.CellBuffer, x, y, width int, ctx PaintContext, fg, muted, bg cell.Color) {
	if width < 1 {
		return
	}
	value := ff.field.Value.String()
	text := value
	indicator := ""
	switch ff.field.Type {
	case FieldSelect:
		for _, option := range ff.field.Options {
			if option.ID == value {
				text = option.Label
				break
			}
		}
		if ff.selectOpen {
			indicator = "▴"
		} else {
			indicator = "▾"
		}
	case FieldMultiSelect:
		labels := make([]string, 0, len(ff.field.Value.Strings()))
		for _, selectedID := range ff.field.Value.Strings() {
			for _, option := range ff.field.Options {
				if option.ID == selectedID {
					labels = append(labels, option.Label)
					break
				}
			}
		}
		text = strings.Join(labels, ", ")
		if ff.selectOpen {
			indicator = "▴"
		} else {
			indicator = "▾"
		}
	case FieldCycle:
		text = ff.cycleDisplay()
	case FieldToggle, FieldCheckbox:
		if ff.field.Value.Bool() {
			text = "[x]"
		} else {
			text = "[ ]"
		}
	case FieldNav:
		text = ff.field.Label
		if ff.field.Description != "" {
			text += "  " + ff.field.Description
		}
		text += "  >"
	}
	if text == "" {
		text = ff.field.Placeholder
	}
	if text == "" {
		text = "Not configured"
		fg = muted
	}
	if ff.compact {
		buf.WriteString(x, y, rendering.TruncateVisual(text, max(1, width-rendering.VisibleWidth(indicator)-1), "…"), fg, bg, 0)
		if indicator != "" {
			buf.WriteString(x+width-rendering.VisibleWidth(indicator), y, indicator, muted, bg, 0)
		}
		return
	}
	FillRect(buf, 0, 0, Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	textX, textW := x, width
	if indicator != "" {
		borderFG := ff.tokensColor(ColorPopupBorder, muted)
		if ff.selectOpen {
			borderFG = ff.tokensColor(ColorPopupBorderFocused, borderFG)
		}
		vBorder := FirstRune(DefaultGlyphsFromTokens(ff.tokens).BorderV, '│')
		buf.Set(x, y, cell.NewCell(vBorder, borderFG, bg, 0))
		if width >= 2 {
			buf.Set(x+width-1, y, cell.NewCell(vBorder, borderFG, bg, 0))
		}
		textX = x + 1
		textW = max(1, width-2)
	}
	if indicator == "" {
		buf.WriteString(x, y, rendering.TruncateVisual(text, width, "…"), fg, bg, 0)
		return
	}
	indicatorW := rendering.VisibleWidth(indicator)
	labelW := max(1, textW-indicatorW-1)
	buf.WriteString(textX, y, rendering.TruncateVisual(text, labelW, "…"), fg, bg, 0)
	buf.WriteString(textX+textW-indicatorW, y, indicator, muted, bg, 0)
}

func (ff *FormField) paintColorValue(buf *surface.CellBuffer, x, y, width int, fg, muted, bg cell.Color) {
	if width < 1 {
		return
	}
	hex := ff.field.Value.String()
	swatchFG := rendering.LipglossColorToCell(hex)
	if swatchFG.IsDefault() {
		swatchFG = fg
	}
	swatch := "██"
	rest := " " + hex + "  (preset)"
	used := rendering.VisibleWidth(swatch)
	buf.WriteString(x, y, rendering.TruncateVisual(swatch, width, ""), swatchFG, bg, 0)
	if used < width {
		buf.WriteString(x+used, y, rendering.TruncateVisual(rest, width-used, ""), muted, bg, 0)
	}
}

func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func (ff *FormField) tokensColor(role SemanticColor, fallback cell.Color) cell.Color {
	if ff.tokens == nil {
		return fallback
	}
	return ff.tokens.Color(role)
}

func (ff *FormField) Handle(ctx EventContext, evt Event) EventResult {
	if ff.field.Disabled || ff.field.ReadOnly {
		return EventResult{}
	}
	switch ff.field.Type {
	case FieldText, FieldPassword:
		before := ff.control.Value()
		result := ff.control.Handle(ff.controlContext(ctx), evt)
		if ff.control.Value() != before {
			ff.setValue(StringValue(ff.control.Value()))
			if ff.field.Live {
				// Live fields preview per keystroke: every value change
				// emits form.changed so hosts can apply it immediately.
				result.Action = &Action{ID: "form.changed", Payload: ff.field.ID}
				result.Invalidate |= InvalidateLayout | InvalidatePaint
			}
		}
		return result
	case FieldTextArea:
		if ff.area == nil {
			return EventResult{}
		}
		before := ff.area.Value()
		result := ff.area.Handle(ff.controlContext(ctx), evt)
		if ff.area.Value() != before {
			ff.setValue(StringValue(ff.area.Value()))
		}
		return result
	case FieldKeyValues:
		if ff.grid == nil {
			return EventResult{}
		}
		before := ff.grid.Value()
		result := ff.grid.Handle(ChildEventContext(ctx, ff.grid.Bounds()), evt)
		if ff.grid.Value() != before {
			// Row count can change, so the field re-measures.
			ff.setValue(StringValue(ff.grid.Value()))
			result.Handled = true
			result.Invalidate |= InvalidateLayout | InvalidatePaint
		}
		return result
	case FieldSelect, FieldMultiSelect:
		return ff.handleSelect(ctx, evt)
	case FieldCycle:
		return ff.handleCycle(ctx, evt)
	case FieldToggle, FieldCheckbox:
		return ff.handleToggle(evt)
	case FieldNav:
		if evt.IsKey() && (evt.Key.Code == tea.KeyEnter || evt.Key.Code == tea.KeySpace || evt.Key.Code == tea.KeyRight) {
			return EventResult{Handled: true, Action: &Action{ID: ff.field.Target, Payload: ff.field.ID}}
		}
		if evt.Kind == EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
			return EventResult{Handled: true, Action: &Action{ID: ff.field.Target, Payload: ff.field.ID}, Invalidate: InvalidateFocus | InvalidatePaint}
		}
	}
	return EventResult{}
}

func (ff *FormField) paintCompactRow(buf *surface.CellBuffer, x, y, width int, ctx PaintContext, labelFG, valueFG, muted, bg cell.Color) {
	if ff.field == nil {
		return
	}
	if ff.field.Disabled {
		labelFG = muted
		valueFG = muted
	}
	focused := ctx.Focused && !ff.field.Disabled && !ff.field.skipFocus()
	hovered := ctx.Hovered && !ff.field.Disabled && !ff.field.skipFocus() && !focused
	rowBG := bg
	if focused || hovered {
		rowBG = ff.tokensColor(ColorHover, bg)
		FillRect(buf, 0, 0, Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(rowBG))
	}
	if focused {
		labelFG = ff.tokensColor(ColorAccent, labelFG)
	}
	chevron := string(FirstRune(DefaultGlyphsFromTokens(ff.tokens).TreeCollapsed, '▸'))
	chevronW := rendering.VisibleWidth(chevron)
	if ff.field.Type == FieldNav && chevronW > 0 && chevronW <= width {
		chevronFG := muted
		if focused {
			chevronFG = labelFG
		}
		buf.WriteString(x+width-chevronW, y, chevron, chevronFG, rowBG, 0)
	}
	label := rendering.TruncateVisual(ff.field.Label, max(1, ff.labelWidth()), "…")
	labelAttr := cell.Attrs(0)
	if focused {
		labelAttr = cell.AttrBold
	}
	buf.WriteString(x, y, label, labelFG, rowBG, labelAttr)
	raw := ff.compactColumnsRaw()
	if len(raw) == 0 {
		return
	}
	cells, colMins := ff.compactCellMins(raw)
	inherited := ff.field.Inherited
	// Reserve the nav chevron plus a small gutter on every compact row so
	// Multi Agent's inherited column lands on the same cells as the agent
	// rows and the trailing meta text ("inherited from workspace") never
	// hugs the chevron or the focused row's right edge.
	trailerW := chevronW + TableMinGap
	plan := planCompactTable(width, ff.labelWidth(), trailerW, colMins)
	ff.paintTableCells(buf, x, y, width-trailerW, plan, cells, inherited, valueFG, muted, rowBG)
}

func (ff *FormField) paintTableCells(buf *surface.CellBuffer, x, y, width int, plan compactTablePlan, cells []string, inherited bool, valueFG, muted, bg cell.Color) {
	if len(cells) == 0 {
		return
	}
	sepW := rendering.VisibleWidth(TableSep)
	col := plan.labelW + plan.gapL
	last := len(cells) - 1
	for i, text := range cells {
		if i > 0 {
			if i == last {
				col += plan.flex
			}
			if col+sepW > width {
				return
			}
			if strings.TrimSpace(text) != "" {
				buf.WriteString(x+col, y, TableSep, muted, bg, 0)
			}
			col += sepW
		}
		w := plan.colW[i]
		if strings.TrimSpace(text) == "" {
			col += w
			continue
		}
		if w < 1 || col >= width {
			return
		}
		fg := valueFG
		if inherited && i == last {
			fg = muted
		}
		buf.WriteString(x+col, y, rendering.TruncateVisual(text, min(w, width-col), "…"), fg, bg, 0)
		col += w
	}
}

func (ff *FormField) compactColumnsRaw() []string {
	if ff.field == nil {
		return nil
	}
	switch ff.field.Type {
	case FieldNav:
		if cols := ff.field.tableColumns(); len(cols) > 0 {
			return cols
		}
		text := strings.TrimSpace(ff.field.Description)
		if text == "" {
			text = strings.TrimSpace(ff.field.Value.String())
		}
		cols := make([]string, 0, 2)
		if text != "" {
			cols = append(cols, text)
		}
		if ff.field.Inherited {
			if suffix := ff.inheritedSuffix(); suffix != "" {
				cols = append(cols, suffix)
			}
		}
		return cols
	case FieldCycle:
		// Columns act as spacer slots before the cycle control so the
		// control can align with a specific shared table column (e.g. the
		// · provider column) instead of hugging the label.
		cols := append(append([]string(nil), ff.field.Columns...), ff.cycleDisplay())
		if ff.field.Inherited {
			if suffix := ff.inheritedSuffix(); suffix != "" {
				cols = append(cols, suffix)
			}
		}
		return cols
	case FieldText, FieldPassword:
		// Single-line editor rows join the shared compact table with one
		// value cell so their editors start at the same label→value boundary
		// as the nav/cycle rows around them (e.g. "Compact context at  85").
		// Inheritance meta stays off editor rows; the editor paints the value
		// alone. The cell feeds Form.tableColumnWidths so a long value widens
		// the shared column for every row instead of clipping.
		return []string{ff.field.Value.String()}
	default:
		return nil
	}
}

// compactCellMins pads one row's cells to the form's shared column count and
// merges their widths into the form-wide minimums, mirroring what
// Form.tableColumnWidths measured for the whole form. Every compact row plans
// against the same merged widths so a column never drifts between rows.
func (ff *FormField) compactCellMins(raw []string) ([]string, []int) {
	inherited := ff.field != nil && ff.field.Inherited
	cells := padInheritedColumns(raw, inherited, max(len(ff.colWidths), len(raw)))
	colMins := append([]int(nil), ff.colWidths...)
	for len(colMins) < len(cells) {
		colMins = append(colMins, 0)
	}
	for i, text := range cells {
		colMins[i] = max(colMins[i], rendering.VisibleWidth(text))
	}
	return cells, colMins
}

// compactEditorX returns the field-relative x where a compact single-line
// editor row (text/password) must place its control so the value lands in the
// form's shared second column: exactly the first cell of the compact-table
// plan every nav/cycle row paints. It returns -1 for rows that do not
// participate (other form densities or field types, or rows too narrow to
// hold the table) so callers fall back to the label-hugging control geometry.
func (ff *FormField) compactEditorX(width int) int {
	if !ff.compact || ff.field == nil {
		return -1
	}
	if ff.field.Type != FieldText && ff.field.Type != FieldPassword {
		return -1
	}
	_, colMins := ff.compactCellMins(ff.compactColumnsRaw())
	chevronW := rendering.VisibleWidth(string(FirstRune(DefaultGlyphsFromTokens(ff.tokens).TreeCollapsed, '▸')))
	plan := planCompactTable(width, ff.labelWidth(), chevronW+TableMinGap, colMins)
	if x0 := plan.labelW + plan.gapL; x0 < width-1 {
		return x0
	}
	return -1
}

func (ff *FormField) cycleDisplay() string {
	if ff.field == nil {
		return ""
	}
	text := ff.field.Value.String()
	for _, option := range ff.field.Options {
		if option.ID == text {
			text = option.Label
			break
		}
	}
	if text == "" {
		text = ff.field.Placeholder
	}
	if text == "" {
		text = "Not set"
	}
	g := DefaultGlyphsFromTokens(ff.tokens)
	return g.CycleLeft + " " + text + " " + g.CycleRight
}

// inputWidth sizes a spacious single-line input to its content class instead
// of stretching every box across the pane: numbers and unit fields get a
// short box, other text a readable line, so adjacent inputs never fuse into
// one slab and the eye finds each value.
func (ff *FormField) inputWidth(available int) int {
	value := strings.TrimSpace(ff.field.Value.String())
	if ff.field.Unit != "" || (value != "" && isNumeric(value)) {
		return min(available, max(12, rendering.VisibleWidth(value)+4))
	}
	return min(available, 56)
}

func isNumeric(value string) bool {
	digits := 0
	for index, r := range value {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.' || (index == 0 && r == '-'):
		default:
			return false
		}
	}
	return digits > 0
}

type cycleSegment struct {
	x, w  int
	index int
}

// cycleSegments lays out every option of a segmented field inline, relative
// to the control's content x. It returns nil when the options cannot all fit,
// in which case the control falls back to the ‹ value › cycle.
func (ff *FormField) cycleSegments(width int) []cycleSegment {
	if ff.field.Style != StyleSegmented || len(ff.field.Options) < 2 {
		return nil
	}
	// Segments touch: together they read as one pill-shaped control.
	const gap = 0
	segments := make([]cycleSegment, 0, len(ff.field.Options))
	x := 0
	for index, option := range ff.field.Options {
		w := rendering.VisibleWidth(option.Label) + 2
		if index > 0 {
			x += gap
		}
		segments = append(segments, cycleSegment{x: x, w: w, index: index})
		x += w
	}
	if x > width {
		return nil
	}
	return segments
}

// cycleClickTarget resolves a pointer click on a cycle control to the option
// it selects. Segments select themselves; the ‹ half of a stepper steps back
// and the › half steps forward; a switch flips from anywhere on its row. ok
// is false for a click beside a stepper or segments (the label column or past
// the control's end): that click only focuses the row, never guessing a
// direction.
func (ff *FormField) cycleClickTarget(localX int) (int, bool) {
	n := len(ff.field.Options)
	current := ff.selectedIndex()
	next, previous := (current+1)%n, (current-1+n)%n
	width := ff.Bounds().W
	if !ff.compact {
		// A switch's whole row is its label: any click flips it.
		if ff.field.Style == StyleSwitch {
			return next, true
		}
		x0, w := ff.valueX(width), ff.boxWidth(width)
		if localX < x0 || localX >= x0+w {
			return current, false
		}
		switch {
		case !ff.isStepper(width):
			for _, segment := range ff.cycleSegments(max(1, width-x0)) {
				if localX >= x0+segment.x && localX < x0+segment.x+segment.w {
					return segment.index, true
				}
			}
			return current, false
		case localX < x0+w/2:
			return max(0, current-1), true
		default:
			return min(n-1, current+1), true
		}
	}
	x0, spanW := ff.cycleSpan(width)
	if ff.field.Style == StyleSwitch || x0 < 0 {
		return next, true
	}
	g := DefaultGlyphsFromTokens(ff.tokens)
	leftW := rendering.VisibleWidth(g.CycleLeft)
	switch {
	case localX < x0 || localX >= x0+spanW:
		return current, false
	case localX < x0+leftW+1:
		return previous, true
	}
	return next, true
}

// cycleSpan returns the field-relative x and painted width of the ‹ value ›
// stepper, for both spacious and compact (table) rows; x is -1 when the row
// cannot place it.
func (ff *FormField) cycleSpan(width int) (int, int) {
	g := DefaultGlyphsFromTokens(ff.tokens)
	raw := ff.compactColumnsRaw()
	if len(raw) == 0 {
		return -1, 0
	}
	cells, colMins := ff.compactCellMins(raw)
	chevronW := rendering.VisibleWidth(string(FirstRune(g.TreeCollapsed, '▸')))
	trailerW := chevronW + TableMinGap
	plan := planCompactTable(width, ff.labelWidth(), trailerW, colMins)
	target := len(ff.field.Columns)
	sepW := rendering.VisibleWidth(TableSep)
	col := plan.labelW + plan.gapL
	last := len(cells) - 1
	for i := range cells {
		if i > 0 {
			if i == last {
				col += plan.flex
			}
			col += sepW
		}
		if i == target {
			if i >= len(plan.colW) {
				return -1, 0
			}
			return col, min(plan.colW[i], rendering.VisibleWidth(cells[i]))
		}
		if i < len(plan.colW) {
			col += plan.colW[i]
		}
	}
	return -1, 0
}

// cycleLabel resolves the current option's display label.
func (ff *FormField) cycleLabel() string {
	value := ff.field.Value.String()
	for _, option := range ff.field.Options {
		if option.ID == value {
			return option.Label
		}
	}
	if ff.field.Placeholder != "" {
		return ff.field.Placeholder
	}
	if value == "" {
		return "Not set"
	}
	return value
}

func padInheritedColumns(cols []string, inherited bool, width int) []string {
	if len(cols) == 0 {
		return cols
	}
	if width < len(cols) {
		width = len(cols)
	}
	if inherited && len(cols) < width {
		head := append([]string(nil), cols[:len(cols)-1]...)
		for len(head) < width-1 {
			head = append(head, "")
		}
		return append(head, cols[len(cols)-1])
	}
	out := append([]string(nil), cols...)
	for len(out) < width {
		out = append(out, "")
	}
	return out
}

func (ff *FormField) inheritedSuffix() string {
	if ff.field == nil || !ff.field.Inherited {
		return ""
	}
	if ff.field.InheritedFrom != "" {
		return "inherited from " + ff.field.InheritedFrom
	}
	return "inherited"
}

func (ff *FormField) navDetail() string {
	if ff.field == nil {
		return ""
	}
	detail := strings.TrimSpace(ff.field.Description)
	if ff.hideDescription && !ff.field.DescriptionImportant {
		detail = ""
	}
	suffix := ff.inheritedSuffix()
	if suffix == "" {
		return detail
	}
	if detail == "" {
		return suffix
	}
	return detail + "  ·  " + suffix
}

func (ff *FormField) handleSelect(ctx EventContext, evt Event) EventResult {
	if evt.IsKey() {
		key := evt.Key
		if key.Code == tea.KeyEsc && ff.selectOpen {
			// Esc clears the filter first (when non-empty) so the user can
			// back out of a narrowed list without closing the dropdown. The
			// FilterField swallows Esc only when it has text; when the
			// filter is empty it returns unhandled and we close here.
			if ff.selectFilter != nil && ff.selectFilter.Value() != "" {
				before := ff.selectFilter.Value()
				result := ff.selectFilter.Handle(ctx, evt)
				if ff.selectFilter.Value() != before {
					return result
				}
			}
			ff.closeSelect()
			return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
		}
		if !ff.selectOpen && (key.Code == tea.KeyEnter || key.Code == tea.KeySpace) {
			ff.selectOpen = true
			ff.syncSelectItems(true)
			return EventResult{Handled: true, Action: &Action{ID: "form.select.open", Payload: ff.field.ID}, Invalidate: InvalidateLayout | InvalidatePaint}
		}
	}
	if evt.Kind == EventMouseClick && evt.MouseClick.Button == tea.MouseLeft && !ff.selectOpen {
		ff.selectOpen = true
		ff.syncSelectItems(true)
		return EventResult{Handled: true, Action: &Action{ID: "form.select.open", Payload: ff.field.ID}, Invalidate: InvalidateLayout | InvalidatePaint}
	}
	if !ff.selectOpen || ff.selectList == nil {
		return EventResult{}
	}
	if evt.Kind == EventMouseMotion {
		// Hover only over visible option rows, never the label column.
		lb := ff.selectList.Bounds()
		if ctx.LocalX < lb.X || ctx.LocalX >= lb.X+lb.W || ctx.LocalY < lb.Y || ctx.LocalY >= lb.Y+lb.H {
			return EventResult{Handled: true}
		}
		return ff.selectList.Handle(ff.selectContext(ctx), evt)
	}
	if evt.Kind == EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		switch {
		case !ff.selectPanelContains(ctx.LocalX, ctx.LocalY):
			// Outside the dropdown panel: cancel without committing and
			// swallow the click so it cannot act on anything underneath.
			ff.closeSelect()
			return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
		case ctx.LocalY == 0:
			// Control row while open toggles the dropdown shut.
			ff.closeSelect()
			return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
		case ff.selectFilter != nil && ff.filterContains(ctx.LocalX, ctx.LocalY):
			return ff.selectFilter.Handle(ff.filterContext(ctx), evt)
		default:
			lb := ff.selectList.Bounds()
			if ctx.LocalX < lb.X || ctx.LocalX >= lb.X+lb.W || ctx.LocalY < lb.Y || ctx.LocalY >= lb.Y+lb.H {
				// Panel padding below the visible rows is decorative.
				return EventResult{Handled: true}
			}
		}
	}
	if len(ff.field.Options) == 0 {
		if evt.IsKey() && (evt.Key.Code == tea.KeyEnter || evt.Key.Code == tea.KeySpace) && !ff.field.Loading {
			ff.selectOpen = false
			return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
		}
		// Let non-key/non-mouse events (e.g. asynchronous ModelsMsg) bubble
		// through to the screen-level eventTarget so model.Update can clear
		// the Loading flag and populate options. Claiming Handled here would
		// swallow the result and leave the UI stuck on "Fetching models...".
		if !evt.IsKey() && !evt.IsMouse() {
			return EventResult{}
		}
		return EventResult{Handled: true}
	}
	// Click on the filter row places the filter's text cursor.
	if evt.Kind == EventMouseClick && ff.selectFilter != nil && ff.filterContains(ctx.LocalX, ctx.LocalY) {
		return ff.selectFilter.Handle(ff.filterContext(ctx), evt)
	}
	// Keys go to the filter first — typing narrows the list. Arrow keys,
	// Enter, and Esc-with-empty-filter fall through to the list.
	if evt.IsKey() && ff.selectFilter != nil {
		filterCtx := ctx
		filterCtx.Focused = true
		before := ff.selectFilter.Value()
		result := ff.selectFilter.Handle(filterCtx, evt)
		if result.Action != nil && result.Action.ID == "form.select.commit" {
			return ff.commitSelect()
		}
		if ff.selectFilter.Value() != before {
			return result
		}
		if result.Handled {
			return result
		}
	}
	result := ff.selectList.Handle(ff.selectContext(ctx), evt)
	if result.Action != nil && result.Action.ID == "form.select.commit" {
		return ff.commitSelect()
	}
	return result
}

// commitSelect applies the list's current selection to the field value and
// returns the form.changed action result. For FieldSelect the dropdown closes
// and the filter/preview state resets; for FieldMultiSelect the dropdown stays
// open and the filter is preserved so the user can toggle multiple matching
// items without retyping the query.
func (ff *FormField) commitSelect() EventResult {
	if selected, ok := ff.selectList.Selected(); ok {
		if id, ok := selected.Payload.(string); ok {
			if ff.field.Type == FieldMultiSelect {
				values := append([]string(nil), ff.field.Value.Strings()...)
				found := -1
				for index, value := range values {
					if value == id {
						found = index
						break
					}
				}
				if found >= 0 {
					values = append(values[:found], values[found+1:]...)
				} else {
					values = append(values, id)
				}
				ff.setValue(StringsValue(values))
			} else {
				ff.setValue(StringValue(id))
			}
		}
	}
	// FieldSelect closes after commit. MultiSelect stays open with its filter
	// so several matches can be toggled in one search.
	if ff.field.Type == FieldSelect {
		ff.selectOpen = false
		ff.selectQuery = ""
		if ff.selectFilter != nil {
			ff.selectFilter.SetValue("")
		}
	}
	return EventResult{Handled: true, Action: &Action{ID: "form.changed", Payload: ff.field.ID}, Invalidate: InvalidateLayout | InvalidatePaint}
}

// resetSelectPopup clears the filter query so the next open starts from a
// clean list. Called on Esc-close.
func (ff *FormField) resetSelectPopup() {
	ff.selectQuery = ""
	if ff.selectFilter != nil {
		ff.selectFilter.SetValue("")
	}
}

// closeSelect drops the dropdown without committing anything.
func (ff *FormField) closeSelect() {
	ff.selectOpen = false
	ff.resetSelectPopup()
}

// selectPanelContains reports whether field-local (x,y) sits inside the open
// dropdown panel: the control row plus the filter row and the visible option
// rows. Clicks outside this region must cancel, never select.
func (ff *FormField) selectPanelContains(x, y int) bool {
	if !ff.selectOpen {
		return false
	}
	controlX, controlW := ff.selectPanel(ff.Bounds().W)
	y0, y1 := 0, 1
	if ff.overlayY < 0 {
		y0 = ff.overlayY
	} else {
		y1 = ff.overlayY + ff.selectPanelHeight()
	}
	return x >= controlX && x < controlX+controlW && y >= y0 && y < y1
}

func (ff *FormField) filterContains(x, y int) bool {
	if ff.selectFilter == nil {
		return false
	}
	r := ff.selectFilter.Bounds()
	return r.Contains(x, y)
}

func (ff *FormField) filterContext(ctx EventContext) EventContext {
	if ff.selectFilter == nil {
		return ctx
	}
	r := ff.selectFilter.Bounds()
	ctx.LocalX -= r.X
	ctx.LocalY -= r.Y
	return ctx
}

func (ff *FormField) handleCycle(ctx EventContext, evt Event) EventResult {
	if evt.Kind == EventMouseClick && evt.MouseClick.Button == tea.MouseLeft && len(ff.field.Options) > 0 {
		idx, ok := ff.cycleClickTarget(ctx.LocalX)
		if !ok {
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
		ff.setValue(StringValue(ff.field.Options[idx].ID))
		return EventResult{
			Handled:    true,
			Action:     &Action{ID: "form.changed", Payload: ff.field.ID},
			Invalidate: InvalidatePaint,
		}
	}
	if evt.Kind != EventKey {
		return EventResult{}
	}
	key := evt.Key
	if (key.Code != tea.KeyLeft && key.Code != tea.KeyRight && key.Code != tea.KeyEnter && key.Code != tea.KeySpace) || len(ff.field.Options) == 0 {
		return EventResult{}
	}
	idx := ff.selectedIndex()
	n := len(ff.field.Options)
	switch {
	case ff.clamps() && key.Code == tea.KeyLeft:
		// A stepper is an ordered scale: its ends hold instead of wrapping.
		idx = max(0, idx-1)
	case ff.clamps() && key.Code == tea.KeyRight:
		idx = min(n-1, idx+1)
	case key.Code == tea.KeyLeft:
		idx = (idx - 1 + n) % n
	default:
		idx = (idx + 1) % n
	}
	ff.setValue(StringValue(ff.field.Options[idx].ID))
	return EventResult{
		Handled:    true,
		Action:     &Action{ID: "form.changed", Payload: ff.field.ID},
		Invalidate: InvalidatePaint,
	}
}

// clamps reports whether arrow keys hold at the ends of the options: true
// for a spacious ‹ value › stepper, whose arrows read as less and more.
func (ff *FormField) clamps() bool {
	return !ff.compact && ff.isStepper(ff.Bounds().W)
}

func (ff *FormField) handleToggle(evt Event) EventResult {
	if evt.Kind == EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		ff.setValue(BoolValue(!ff.field.Value.Bool()))
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	}
	if evt.Kind != EventKey || (evt.Key.Code != tea.KeyEnter && evt.Key.Code != tea.KeySpace) {
		return EventResult{}
	}
	ff.setValue(BoolValue(!ff.field.Value.Bool()))
	return EventResult{Handled: true, Invalidate: InvalidatePaint}
}

func (ff *FormField) setValue(value FieldValue) {
	if ff.model != nil {
		ff.model.SetValue(ff.field.ID, value)
		return
	}
	ff.field.Value = value
	ff.field.Dirty = !ff.field.Value.Equal(ff.field.Initial)
	ff.field.Validate()
}

func (ff *FormField) selectedIndex() int {
	for i, option := range ff.field.Options {
		if option.ID == ff.field.Value.String() {
			return i
		}
	}
	return 0
}

func (ff *FormField) syncSelectItems(preserveCursor bool) {
	if ff.selectList == nil {
		return
	}
	selectedID := ""
	if selected, ok := ff.selectList.Selected(); ok && selected.Selectable() {
		selectedID = selected.ID
	} else if ff.selectQuery == "" && ff.field.Type == FieldSelect {
		// A loading/empty placeholder is not a user cursor choice. Anchor the
		// first real response to the selected value, even if it is not row zero.
		selectedID = ff.field.Value.String()
	}
	options := ff.filteredOptions()
	items := make([]ListItem, 0, len(options))
	for _, option := range options {
		icon := ""
		if ff.field.Type == FieldMultiSelect {
			icon = "[ ]"
			for _, selectedID := range ff.field.Value.Strings() {
				if selectedID == option.ID {
					icon = "[x]"
					break
				}
			}
		}
		items = append(items, ListItem{ID: option.ID, Label: option.Label, Detail: option.Detail, Icon: icon, Disabled: option.Disabled, Payload: option.ID})
	}
	changed := !sameFormOptions(ff.selectList.Items(), items)
	if changed {
		ff.selectList.SetItems(items)
		for index, item := range items {
			if item.ID == selectedID {
				ff.selectList.SelectIndex(index)
				break
			}
		}
	}
	// Opening and filtering a select must retain its list cursor. Re-anchoring
	// to the persisted field value here would make every filter keystroke jump
	// back to that value and can visually imply an uncommitted selection.
	if ff.field.Type == FieldSelect && !preserveCursor {
		ff.selectList.SelectIndex(ff.selectedIndex())
	}
}

// filteredOptions returns the field's select options narrowed by the active
// filter query. Matching is a case-insensitive substring test against each
// option's ID, Label, and Detail so models can be found by API ID, vendor
// name, or context-window hint.
func (ff *FormField) filteredOptions() []FormOption {
	q := strings.ToLower(strings.TrimSpace(ff.selectQuery))
	if q == "" {
		return ff.field.Options
	}
	out := make([]FormOption, 0, len(ff.field.Options))
	for _, o := range ff.field.Options {
		if strings.Contains(strings.ToLower(o.ID), q) ||
			strings.Contains(strings.ToLower(o.Label), q) ||
			strings.Contains(strings.ToLower(o.Detail), q) {
			out = append(out, o)
		}
	}
	return out
}

// applySelectFilter is the OnChanged callback for the dropdown's filter field.
// It records the query and re-syncs the list without changing the field value.
func (ff *FormField) applySelectFilter(query string) {
	ff.selectQuery = query
	ff.syncSelectItems(true)
}

func sameFormOptions(current, next []ListItem) bool {
	if len(current) != len(next) {
		return false
	}
	for index := range current {
		if current[index].ID != next[index].ID || current[index].Label != next[index].Label || current[index].Detail != next[index].Detail || current[index].Icon != next[index].Icon || current[index].Disabled != next[index].Disabled {
			return false
		}
	}
	return true
}

func (ff *FormField) selectRows() int {
	if !ff.selectOpen {
		return 0
	}
	return 1 /* filter row */ + max(1, min(len(ff.filteredOptions()), 5))
}

func (ff *FormField) controlContext(ctx EventContext) EventContext {
	if ff.control != nil {
		r := ff.control.Bounds()
		ctx.LocalX -= r.X
		ctx.LocalY -= r.Y
		return ctx
	}
	if ff.area != nil {
		r := ff.area.Bounds()
		ctx.LocalX -= r.X
		ctx.LocalY -= r.Y
		return ctx
	}
	return ctx
}

func (ff *FormField) selectContext(ctx EventContext) EventContext {
	r := ff.selectList.Bounds()
	ctx.LocalX -= r.X
	ctx.LocalY -= r.Y
	return ctx
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
