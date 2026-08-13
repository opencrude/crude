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
	labelW  int

	selectOpen   bool
	selectList   *List
	selectFilter *FilterField
	selectQuery  string
	overlayY     int
}

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

func (ff *FormField) SetTokens(ts *TokenSet) {
	ff.tokens = ts
	if ff.control != nil {
		ff.control.SetTokens(ts)
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
	ff.syncSelectItems(ff.selectOpen)
}

// Children is intentionally empty. FormField retains its input/list controls
// so the enclosing FormBody owns one form focus scope and action route.
func (ff *FormField) Children() []Component { return nil }

func (ff *FormField) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: ff.height()})
}

func (ff *FormField) height() int {
	if ff.field.Type == FieldSeparator {
		return 1
	}
	if ff.field.Type == FieldNav {
		if strings.TrimSpace(ff.navDetail()) != "" {
			return 2
		}
		return 1
	}
	h := 1
	if ff.field.Description != "" {
		h++
	}
	h += len(ff.field.Errors)
	return h
}

func (ff *FormField) Layout(ctx LayoutContext, r Rect) {
	ff.SetBounds(r)
	controlX, controlW := ff.controlX(r.W), ff.controlWidth(r.W)
	if ff.control != nil {
		ff.control.Layout(ctx, Rect{X: controlX + 1, Y: 0, W: max(1, controlW-2), H: 1})
	}
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
	controlX, controlW := ff.controlX(r.W), ff.controlWidth(r.W)
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
	if ff.field != nil && (ff.field.Type == FieldSeparator || ff.field.Type == FieldNav) {
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
		bg = ff.tokens.Color(ColorPopupBg)
		labelFG = ff.tokens.Color(ColorMuted)
		valueFG = ff.tokens.Color(ColorFg)
		mutedFG = ff.tokens.Color(ColorMuted)
		errorFG = ff.tokens.Color(ColorError)
		dividerFG = ff.tokens.Color(ColorPopupDivider)
	}
	// Form focus is deliberately quieter than a list selection. The row gets a
	// subtle control surface, while the marker and label carry the accent. The
	// description remains quiet so focus reads as typographic emphasis rather
	// than a large selection block.
	focusedRow := ctx.Focused && !ff.field.Disabled && ff.field.Type != FieldSeparator
	rowBG := bg
	if focusedRow {
		rowBG = ff.tokensColor(ColorPopupSubtleBg, bg)
		labelFG = ff.tokensColor(ColorAccent, labelFG)
		valueFG = ff.tokensColor(ColorFg, valueFG)
		mutedFG = ff.tokensColor(ColorMuted, mutedFG)
	}
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	x, y := originX+r.X, originY+r.Y
	if ff.field.Type == FieldSeparator {
		ff.paintSeparator(buf, x, y, r.W, mutedFG, dividerFG, bg)
		return
	}
	if ff.field.Type == FieldNav {
		ff.paintNav(buf, x, y, r.W, ctx, labelFG, mutedFG, bg)
		return
	}

	if focusedRow {
		FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(rowBG))
	}
	labelX := x + ff.labelIndent()
	label := rendering.TruncateVisual(ff.field.Label, max(1, ff.labelWidth()-1), "")
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
	buf.WriteString(labelX, y, label, labelFG, rowBG, labelAttr)

	controlX := x + ff.controlX(r.W)
	controlW := ff.controlWidth(r.W)
	contentX := controlX + 1
	contentW := max(1, controlW-2)
	// The focused row is one raised band: control and label share the same
	// background so focus reads as a full-row highlight, not a box.
	controlBG := rowBG
	if !focusedRow {
		controlBG = ff.tokensColor(ColorPopupSubtleBg, bg)
	}
	FillRect(buf, 0, 0, Rect{X: controlX, Y: y, W: controlW, H: 1}, cell.BlankCell(controlBG))

	if ff.field.ReadOnly && isHexColor(ff.field.Value.String()) {
		ff.paintColorValue(buf, contentX, y, contentW, valueFG, mutedFG, controlBG)
	} else if ff.field.Type == FieldText || ff.field.Type == FieldPassword {
		if ff.control != nil {
			fieldCtx := ctx
			fieldCtx.Focused = ctx.Focused
			ff.control.SetBackgroundColor(controlBG)
			ff.control.Paint(buf, originX+r.X, originY+r.Y, fieldCtx)
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
	if ff.field.Description != "" && line < r.H {
		buf.WriteString(contentX, y+line, rendering.TruncateVisual(ff.field.Description, contentW, "…"), mutedFG, bg, 0)
		line++
	}
	for _, err := range ff.field.Errors {
		if line >= r.H {
			break
		}
		buf.WriteString(contentX, y+line, rendering.TruncateVisual(err, contentW, "…"), errorFG, bg, 0)
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
	controlX := x + ff.controlX(r.W)
	controlW := ff.controlWidth(r.W)
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
		if text == "" {
			text = ff.field.Placeholder
		}
		if text == "" {
			text = "Not set"
		}
		left, right := "←", "→"
		middleW := max(1, width-rendering.VisibleWidth(left)-rendering.VisibleWidth(right)-2)
		text = left + " " + rendering.TruncateVisual(text, middleW, "…")
		pad := width - rendering.VisibleWidth(text) - rendering.VisibleWidth(right)
		if pad < 1 {
			pad = 1
		}
		text += strings.Repeat(" ", pad) + right
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
		}
		return result
	case FieldSelect, FieldMultiSelect:
		return ff.handleSelect(ctx, evt)
	case FieldCycle:
		return ff.handleCycle(evt)
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

func (ff *FormField) paintNav(buf *surface.CellBuffer, x, y, width int, ctx PaintContext, fg, muted, bg cell.Color) {
	if width < 1 {
		return
	}
	if ff.field.Disabled {
		fg = muted
	}
	focused := ctx.Focused && !ff.field.Disabled
	rowBG := bg
	if focused {
		rowBG = ff.tokensColor(ColorPopupSubtleBg, bg)
		fg = ff.tokensColor(ColorAccent, fg)
		FillRect(buf, 0, 0, Rect{X: x, Y: y, W: width, H: ff.height()}, cell.BlankCell(rowBG))
	}
	chevron := string(FirstRune(DefaultGlyphsFromTokens(ff.tokens).TreeCollapsed, '▸'))
	chevronW := rendering.VisibleWidth(chevron)
	labelW := max(1, width-chevronW-1)
	label := rendering.TruncateVisual(ff.field.Label, labelW, "…")
	labelAttr := cell.Attrs(0)
	if focused {
		labelAttr = cell.AttrBold
	}
	buf.WriteString(x, y, label, fg, rowBG, labelAttr)
	if chevronW > 0 && chevronW <= width {
		chevronFG := muted
		if focused {
			chevronFG = fg
		}
		buf.WriteString(x+width-chevronW, y, chevron, chevronFG, rowBG, 0)
	}
	detail := ff.navDetail()
	if detail == "" || ff.height() < 2 {
		return
	}
	indent := ff.labelIndent()
	if indent < 1 {
		indent = 1
	}
	buf.WriteString(x+indent, y+1, rendering.TruncateVisual(detail, max(1, width-indent), "…"), muted, rowBG, 0)
}

func (ff *FormField) navDetail() string {
	if ff.field == nil {
		return ""
	}
	detail := strings.TrimSpace(ff.field.Description)
	if !ff.field.Inherited {
		return detail
	}
	suffix := "inherited"
	if ff.field.InheritedFrom != "" {
		suffix = "inherited from " + ff.field.InheritedFrom
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
	w := ff.Bounds().W
	controlX, controlW := ff.controlX(w), ff.controlWidth(w)
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

func (ff *FormField) handleCycle(evt Event) EventResult {
	if evt.Kind == EventMouseClick && evt.MouseClick.Button == tea.MouseLeft && len(ff.field.Options) > 0 {
		idx := (ff.selectedIndex() + 1) % len(ff.field.Options)
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
	if key.Code == tea.KeyLeft {
		idx = (idx - 1 + len(ff.field.Options)) % len(ff.field.Options)
	} else {
		idx = (idx + 1) % len(ff.field.Options)
	}
	ff.setValue(StringValue(ff.field.Options[idx].ID))
	return EventResult{
		Handled:    true,
		Action:     &Action{ID: "form.changed", Payload: ff.field.ID},
		Invalidate: InvalidatePaint,
	}
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
	if selected, ok := ff.selectList.Selected(); ok {
		selectedID = selected.ID
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
	r := ff.control.Bounds()
	ctx.LocalX -= r.X
	ctx.LocalY -= r.Y
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
