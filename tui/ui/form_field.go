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

	selectOpen bool
	selectList *List
}

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
		ff.syncSelectItems()
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
	ff.syncSelectItems()
}

// Children is intentionally empty. FormField retains its input/list controls
// so the enclosing FormBody owns one form focus scope and action route.
func (ff *FormField) Children() []Component { return nil }

func (ff *FormField) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: ff.height()})
}

func (ff *FormField) height() int {
	if ff.field.Type == FieldSeparator || ff.field.Type == FieldNav {
		return 1
	}
	h := 1
	if ff.field.Description != "" {
		h++
	}
	h += len(ff.field.Errors)
	if ff.selectOpen {
		h += ff.selectRows()
	}
	return h
}

func (ff *FormField) Layout(ctx LayoutContext, r Rect) {
	ff.SetBounds(r)
	controlX, controlW := ff.controlX(r.W), ff.controlWidth(r.W)
	if ff.control != nil {
		ff.control.Layout(ctx, Rect{X: controlX + 1, Y: 0, W: max(1, controlW-2), H: 1})
	}
	if ff.selectList != nil && ff.selectOpen {
		ff.selectList.Layout(ctx, Rect{X: controlX, Y: 1, W: controlW, H: ff.selectRows()})
	}
}

func (ff *FormField) controlX(width int) int {
	lw := ff.labelWidth()
	if width-lw-3 < 10 {
		return min(lw, width)
	}
	return lw + 3
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
	sectionFG := cell.NoColor
	if ff.tokens != nil {
		bg = ff.tokens.Color(ColorPopupBg)
		labelFG = ff.tokens.Color(ColorMuted)
		valueFG = ff.tokens.Color(ColorFg)
		mutedFG = ff.tokens.Color(ColorMuted)
		errorFG = ff.tokens.Color(ColorError)
		dividerFG = ff.tokens.Color(ColorPopupDivider)
		sectionFG = ff.tokens.Color(ColorAccent2)
	}
	// Form focus is deliberately quieter than a list selection. The row gets a
	// subtle control surface, while the marker and label carry the accent. The
	// description remains quiet so focus reads as typographic emphasis rather
	// than a large selection block.
	focusedRow := ctx.Focused && !ff.field.Disabled && ff.field.Type != FieldSeparator
	if focusedRow {
		labelFG = ff.tokensColor(ColorAccent, labelFG)
		valueFG = ff.tokensColor(ColorFg, valueFG)
		mutedFG = ff.tokensColor(ColorMuted, mutedFG)
	}
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	x, y := originX+r.X, originY+r.Y
	if ff.field.Type == FieldSeparator {
		ff.paintSeparator(buf, x, y, r.W, sectionFG, dividerFG, bg)
		return
	}
	if ff.field.Type == FieldNav {
		ff.paintNav(buf, x, y, r.W, ctx, labelFG, mutedFG, bg)
		return
	}

	if focusedRow {
		marker := DefaultGlyphsFromTokens(ff.tokens).Prompt
		buf.WriteString(x, y, marker, labelFG, bg, cell.AttrBold)
	}
	labelX := x + 2
	label := ff.field.Label
	if ff.field.Required && label != "" {
		label += " *"
	}
	label = rendering.TruncateVisual(label, max(1, ff.labelWidth()-1), "")
	if (ff.field.Disabled || ff.field.ReadOnly) && !focusedRow {
		labelFG = mutedFG
	}
	labelAttr := cell.Attrs(0)
	if focusedRow {
		labelAttr = cell.AttrBold
	}
	buf.WriteString(labelX, y, label, labelFG, bg, labelAttr)

	controlX := x + ff.controlX(r.W)
	controlW := ff.controlWidth(r.W)
	contentX := controlX + 1
	contentW := max(1, controlW-2)
	controlBG := ff.tokensColor(ColorPopupSubtleBg, bg)
	if focusedRow {
		controlBG = ff.tokensColor(ColorHover, controlBG)
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
				labelFG = mutedFG
			}
			buf.WriteString(contentX, y, rendering.TruncateVisual(text, contentW, "…"), valueFG, controlBG, 0)
		}
	} else {
		ff.paintChoice(buf, contentX, y, contentW, ctx, valueFG, mutedFG, controlBG)
	}

	line := 1
	if ff.selectOpen && ff.selectList != nil {
		if len(ff.field.Options) == 0 {
			message := "No options available"
			if ff.field.Loading {
				message = ff.field.LoadMessage
				if message == "" {
					message = "Loading..."
				}
			}
			buf.WriteString(contentX, y+line, rendering.TruncateVisual(message, contentW, "…"), mutedFG, bg, 0)
		} else {
			ff.selectList.Paint(buf, originX+r.X, originY+r.Y, ctx)
		}
		line += ff.selectRows()
	}
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
	label := strings.TrimSpace(ff.field.Label)
	border := string(FirstRune(DefaultGlyphsFromTokens(ff.tokens).BorderH, '-'))
	if label == "" {
		buf.WriteString(x, y, strings.Repeat(border, width), lineFG, bg, 0)
		return
	}
	labelWidth := rendering.VisibleWidth(label)
	if labelWidth+3 >= width {
		buf.WriteString(x, y, rendering.TruncateVisual(label, width, ""), labelFG, bg, cell.AttrBold)
		return
	}
	buf.WriteString(x, y, label, labelFG, bg, cell.AttrBold)
	lineX := x + labelWidth + 2
	lineW := width - labelWidth - 2
	buf.WriteString(lineX, y, strings.Repeat(border, lineW), lineFG, bg, 0)
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
	if indicator == "" {
		buf.WriteString(x, y, rendering.TruncateVisual(text, width, "…"), fg, bg, 0)
		return
	}
	indicatorW := rendering.VisibleWidth(indicator)
	textW := max(1, width-indicatorW-1)
	buf.WriteString(x, y, rendering.TruncateVisual(text, textW, "…"), fg, bg, 0)
	buf.WriteString(x+width-indicatorW, y, indicator, muted, bg, 0)
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
	if ctx.Focused && !ff.field.Disabled {
		bg = ff.tokensColor(ColorPopupSubtleBg, bg)
		FillRect(buf, 0, 0, Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	}
	detail := ff.field.Description
	if ff.field.Inherited {
		if detail != "" {
			detail += "  "
		}
		detail += "(inherited"
		if ff.field.InheritedFrom != "" {
			detail += " from " + ff.field.InheritedFrom
		}
		detail += ")"
	}
	arrow := " >"
	available := max(1, width-rendering.VisibleWidth(arrow))
	label := rendering.TruncateVisual(ff.field.Label, available, "…")
	line := label
	if detail != "" {
		remaining := available - rendering.VisibleWidth(label) - 2
		if remaining > 0 {
			line += "  " + rendering.TruncateVisual(detail, remaining, "…")
		}
	}
	padding := available - rendering.VisibleWidth(line)
	if padding > 0 {
		line += strings.Repeat(" ", padding)
	}
	buf.WriteString(x, y, line, fg, bg, 0)
	buf.WriteString(x+available, y, arrow, muted, bg, 0)
}

func (ff *FormField) handleSelect(ctx EventContext, evt Event) EventResult {
	if evt.IsKey() {
		key := evt.Key
		if key.Code == tea.KeyEsc && ff.selectOpen {
			ff.selectOpen = false
			return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
		}
		if !ff.selectOpen && (key.Code == tea.KeyEnter || key.Code == tea.KeySpace) {
			ff.selectOpen = true
			ff.syncSelectItems()
			return EventResult{Handled: true, Action: &Action{ID: "form.select.open", Payload: ff.field.ID}, Invalidate: InvalidateLayout | InvalidatePaint}
		}
	}
	if evt.Kind == EventMouseClick && evt.MouseClick.Button == tea.MouseLeft && !ff.selectOpen {
		ff.selectOpen = true
		ff.syncSelectItems()
		return EventResult{Handled: true, Action: &Action{ID: "form.select.open", Payload: ff.field.ID}, Invalidate: InvalidateLayout | InvalidatePaint}
	}
	if !ff.selectOpen || ff.selectList == nil {
		return EventResult{}
	}
	if len(ff.field.Options) == 0 {
		if evt.IsKey() && (evt.Key.Code == tea.KeyEnter || evt.Key.Code == tea.KeySpace) && !ff.field.Loading {
			ff.selectOpen = false
			return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
		}
		return EventResult{Handled: true}
	}
	result := ff.selectList.Handle(ff.selectContext(ctx), evt)
	if result.Action != nil && result.Action.ID == "form.select.commit" {
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
		if ff.field.Type == FieldSelect {
			ff.selectOpen = false
		}
		result.Action = &Action{ID: "form.changed", Payload: ff.field.ID}
		result.Invalidate |= InvalidateLayout | InvalidatePaint
	}
	return result
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

func (ff *FormField) syncSelectItems() {
	if ff.selectList == nil {
		return
	}
	selectedID := ""
	if selected, ok := ff.selectList.Selected(); ok {
		selectedID = selected.ID
	}
	items := make([]ListItem, 0, len(ff.field.Options))
	for _, option := range ff.field.Options {
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
	if ff.field.Type == FieldSelect && (changed || !ff.selectOpen) {
		ff.selectList.SelectIndex(ff.selectedIndex())
	}
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
	return max(1, min(len(ff.field.Options), 6))
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
