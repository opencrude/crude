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
	if f.Type == FieldText || f.Type == FieldPassword {
		ff.control = NewTextField(NewID(id, "input"), tokens)
		ff.control.SetValue(f.Value.String())
		ff.control.SetPassword(f.Type == FieldPassword)
		ff.control.SetReadOnly(f.ReadOnly)
		ff.control.SetDisabled(f.Disabled)
		ff.control.SetSubmitAction("form.next")
	}
	if f.Type == FieldSelect {
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
	if ff.control != nil {
		ff.control.Layout(ctx, Rect{X: ff.controlX(r.W), Y: 0, W: ff.controlWidth(r.W), H: 1})
	}
	if ff.selectList != nil && ff.selectOpen {
		ff.selectList.Layout(ctx, Rect{X: ff.controlX(r.W), Y: 1, W: ff.controlWidth(r.W), H: ff.selectRows()})
	}
}

func (ff *FormField) controlX(width int) int {
	lw := ff.labelWidth()
	if width-lw-2 < 10 {
		return min(lw, width)
	}
	return lw + 2
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
	mutedFG := cell.NoColor
	errorFG := cell.NoColor
	dividerFG := cell.NoColor
	if ff.tokens != nil {
		bg = ff.tokens.Color(ColorPopupBg)
		labelFG = ff.tokens.Color(ColorFg)
		mutedFG = ff.tokens.Color(ColorMuted)
		errorFG = ff.tokens.Color(ColorError)
		dividerFG = ff.tokens.Color(ColorPopupDivider)
	}
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	x, y := originX+r.X, originY+r.Y
	if ff.field.Type == FieldSeparator {
		buf.WriteString(x, y, rendering.TruncateVisual("────────────────────────────────────────────────", r.W, ""), dividerFG, bg, 0)
		return
	}
	if ff.field.Type == FieldNav {
		ff.paintNav(buf, x, y, r.W, ctx, labelFG, mutedFG, bg)
		return
	}

	label := ff.field.Label
	if ff.field.Required && label != "" {
		label += "*"
	}
	label = rendering.TruncateVisual(label, ff.labelWidth(), "")
	if ff.field.Disabled || ff.field.ReadOnly {
		labelFG = mutedFG
	}
	buf.WriteString(x, y, label, labelFG, bg, 0)

	if ff.field.Type == FieldText || ff.field.Type == FieldPassword {
		fieldCtx := ctx
		fieldCtx.Focused = ctx.Focused
		ff.control.Paint(buf, originX+r.X, originY+r.Y, fieldCtx)
	} else {
		ff.paintChoice(buf, x+ff.controlX(r.W), y, ff.controlWidth(r.W), ctx, labelFG, mutedFG, bg)
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
			buf.WriteString(x+ff.controlX(r.W), y+line, rendering.TruncateVisual(message, ff.controlWidth(r.W), ""), mutedFG, bg, 0)
		} else {
			ff.selectList.Paint(buf, originX+r.X, originY+r.Y, ctx)
		}
		line += ff.selectRows()
	}
	if ff.field.Description != "" && line < r.H {
		buf.WriteString(x+ff.controlX(r.W), y+line, rendering.TruncateVisual(ff.field.Description, ff.controlWidth(r.W), ""), mutedFG, bg, 0)
		line++
	}
	for _, err := range ff.field.Errors {
		if line >= r.H {
			break
		}
		buf.WriteString(x+ff.controlX(r.W), y+line, rendering.TruncateVisual(err, ff.controlWidth(r.W), ""), errorFG, bg, 0)
		line++
	}
}

func (ff *FormField) paintChoice(buf *surface.CellBuffer, x, y, width int, ctx PaintContext, fg, muted, bg cell.Color) {
	if width < 1 {
		return
	}
	value := ff.field.Value.String()
	text := value
	switch ff.field.Type {
	case FieldSelect:
		for _, option := range ff.field.Options {
			if option.ID == value {
				text = option.Label
				break
			}
		}
		if ff.selectOpen {
			text += " ▴"
		} else {
			text += " ▾"
		}
	case FieldCycle:
		text = "← " + text + " →"
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
		text = "-"
		fg = muted
	}
	if ctx.Focused && !ff.field.Disabled {
		bg = ff.tokensColor(ColorPopupSubtleBg, bg)
	}
	FillRect(buf, 0, 0, Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	buf.WriteString(x, y, rendering.TruncateVisual(text, width, ""), fg, bg, 0)
}

func (ff *FormField) tokensColor(role SemanticColor, fallback cell.Color) cell.Color {
	if ff.tokens == nil {
		return fallback
	}
	return ff.tokens.Color(role)
}

func (ff *FormField) Handle(ctx EventContext, msg tea.Msg) EventResult {
	if ff.field.Disabled {
		return EventResult{}
	}
	switch ff.field.Type {
	case FieldText, FieldPassword:
		before := ff.control.Value()
		result := ff.control.Handle(ff.controlContext(ctx), msg)
		if ff.control.Value() != before {
			ff.setValue(StringValue(ff.control.Value()))
		}
		return result
	case FieldSelect:
		return ff.handleSelect(ctx, msg)
	case FieldCycle:
		return ff.handleCycle(msg)
	case FieldToggle, FieldCheckbox:
		return ff.handleToggle(msg)
	case FieldNav:
		if key, ok := msg.(tea.KeyPressMsg); ok && (key.Code == tea.KeyEnter || key.Code == tea.KeySpace || key.Code == tea.KeyRight) {
			return EventResult{Handled: true, Action: &Action{ID: ff.field.Target, Payload: ff.field.ID}}
		}
		if click, ok := msg.(tea.MouseClickMsg); ok && click.Button == tea.MouseLeft {
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

func (ff *FormField) handleSelect(ctx EventContext, msg tea.Msg) EventResult {
	if key, ok := msg.(tea.KeyPressMsg); ok {
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
	if click, ok := msg.(tea.MouseClickMsg); ok && click.Button == tea.MouseLeft && !ff.selectOpen {
		ff.selectOpen = true
		ff.syncSelectItems()
		return EventResult{Handled: true, Action: &Action{ID: "form.select.open", Payload: ff.field.ID}, Invalidate: InvalidateLayout | InvalidatePaint}
	}
	if !ff.selectOpen || ff.selectList == nil {
		return EventResult{}
	}
	if len(ff.field.Options) == 0 {
		if key, ok := msg.(tea.KeyPressMsg); ok && (key.Code == tea.KeyEnter || key.Code == tea.KeySpace) && !ff.field.Loading {
			ff.selectOpen = false
			return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
		}
		return EventResult{Handled: true}
	}
	result := ff.selectList.Handle(ff.selectContext(ctx), msg)
	if result.Action != nil && result.Action.ID == "form.select.commit" {
		if selected, ok := ff.selectList.Selected(); ok {
			if id, ok := selected.Payload.(string); ok {
				ff.setValue(StringValue(id))
			}
		}
		ff.selectOpen = false
		result.Action = nil
		result.Invalidate |= InvalidateLayout | InvalidatePaint
	}
	return result
}

func (ff *FormField) handleCycle(msg tea.Msg) EventResult {
	if click, ok := msg.(tea.MouseClickMsg); ok && click.Button == tea.MouseLeft && len(ff.field.Options) > 0 {
		idx := (ff.selectedIndex() + 1) % len(ff.field.Options)
		ff.setValue(StringValue(ff.field.Options[idx].ID))
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || (key.Code != tea.KeyLeft && key.Code != tea.KeyRight && key.Code != tea.KeyEnter && key.Code != tea.KeySpace) || len(ff.field.Options) == 0 {
		return EventResult{}
	}
	idx := ff.selectedIndex()
	if key.Code == tea.KeyLeft {
		idx = (idx - 1 + len(ff.field.Options)) % len(ff.field.Options)
	} else {
		idx = (idx + 1) % len(ff.field.Options)
	}
	ff.setValue(StringValue(ff.field.Options[idx].ID))
	return EventResult{Handled: true, Invalidate: InvalidatePaint}
}

func (ff *FormField) handleToggle(msg tea.Msg) EventResult {
	if click, ok := msg.(tea.MouseClickMsg); ok && click.Button == tea.MouseLeft {
		ff.setValue(BoolValue(!ff.field.Value.Bool()))
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || (key.Code != tea.KeyEnter && key.Code != tea.KeySpace) {
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
	items := make([]ListItem, 0, len(ff.field.Options))
	for _, option := range ff.field.Options {
		items = append(items, ListItem{ID: option.ID, Label: option.Label, Detail: option.Detail, Disabled: option.Disabled, Payload: option.ID})
	}
	changed := !sameFormOptions(ff.selectList.Items(), items)
	if changed {
		ff.selectList.SetItems(items)
	}
	if changed || !ff.selectOpen {
		ff.selectList.SelectIndex(ff.selectedIndex())
	}
}

func sameFormOptions(current, next []ListItem) bool {
	if len(current) != len(next) {
		return false
	}
	for index := range current {
		if current[index].ID != next[index].ID || current[index].Label != next[index].Label || current[index].Detail != next[index].Detail || current[index].Disabled != next[index].Disabled {
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
