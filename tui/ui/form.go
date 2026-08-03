package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// Form is a retained shared-form component. It owns field traversal, select
// expansion, and form-local focus while screens translate its emitted actions.
type Form struct {
	Base
	model   *FormModel
	title   string
	detail  string
	tokens  *TokenSet
	fields  []*FormField
	focused int
}

func NewForm(id ComponentID, model *FormModel, title, detail string, tokens *TokenSet) *Form {
	f := &Form{Base: NewBase(id), model: model, title: title, detail: detail, tokens: tokens, focused: -1}
	f.rebuild()
	return f
}

func (f *Form) SetForm(model *FormModel, title, detail string) {
	f.model, f.title, f.detail = model, title, detail
	f.rebuild()
	// Rebuild replaces fields with empty bounds. Re-layout into the current
	// form rect so the next paint shows fields without waiting for a host
	// InvalidateLayout (category switches often only mark paint dirty).
	if !f.Bounds().Empty() {
		f.Layout(LayoutContext{}, f.Bounds())
	}
}

func (f *Form) SetTokens(tokens *TokenSet) {
	f.tokens = tokens
	for _, field := range f.fields {
		field.SetTokens(tokens)
	}
}

// Field returns the retained field by semantic ID.
func (f *Form) Field(id string) *FormField {
	for _, field := range f.fields {
		if field.FieldState().ID == id {
			return field
		}
	}
	return nil
}

func (f *Form) rebuild() {
	focusedID := ""
	if f.focused >= 0 && f.focused < len(f.fields) {
		focusedID = f.fields[f.focused].FieldState().ID
	}
	f.fields = nil
	if f.model == nil {
		f.focused = -1
		return
	}
	for _, field := range f.model.Fields() {
		f.fields = append(f.fields, NewBoundFormField(NewID(f.ID(), field.ID), f.model, field, f.tokens))
	}
	f.focused = 0
	for index, field := range f.fields {
		if field.FieldState().ID == focusedID {
			f.focused = index
			break
		}
	}
}

// Children is intentionally empty. Form retains its fields so a single focus
// scope controls field traversal and action routing.
func (f *Form) Children() []Component { return nil }

func (f *Form) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: f.PreferredHeight(cs.MaxH)})
}

// PreferredHeight returns the rows required by the current field state,
// including expanded selects and validation messages.
func (f *Form) PreferredHeight(maxHeight int) int {
	height := 3
	for _, field := range f.fields {
		height += field.height()
	}
	return min(maxHeight, height)
}

func (f *Form) Layout(ctx LayoutContext, r Rect) {
	f.SetBounds(r)
	y := 3 // title, description, divider
	labelW := 0
	for _, field := range f.fields {
		field.Layout(ctx, Rect{})
		labelW = max(labelW, rendering.VisibleWidth(field.FieldState().Label)+1)
	}
	labelW = min(labelW, max(8, r.W/2))
	for _, field := range f.fields {
		field.SetLabelWidth(labelW)
		h := field.Measure(ctx, Loose(r.W, r.H)).H
		if y+h > r.H {
			break
		}
		field.Layout(ctx, Rect{X: 0, Y: y, W: r.W, H: h})
		y += h
	}
}

func (f *Form) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := f.Bounds()
	bg, fg, muted, divider := f.color(ColorPopupBg), f.color(ColorFg), f.color(ColorMuted), f.color(ColorPopupDivider)
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	x, y := originX+r.X, originY+r.Y
	buf.WriteString(x, y, rendering.TruncateVisual(f.title, r.W, ""), fg, bg, cell.AttrBold)
	buf.WriteString(x, y+1, rendering.TruncateVisual(f.detail, r.W, ""), muted, bg, 0)
	buf.WriteString(x, y+2, strings.Repeat(string(FirstRune(f.tokens.Glyphs().BorderH, '─')), max(0, r.W)), divider, bg, 0)
	for index, field := range f.fields {
		fieldCtx := ctx
		fieldCtx.Focused = ctx.Focused && index == f.focused
		field.Paint(buf, x, y, fieldCtx)
	}
}

func (f *Form) Handle(ctx EventContext, evt Event) EventResult {
	if len(f.fields) == 0 {
		return EventResult{}
	}
	if evt.Kind == EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		for index, field := range f.fields {
			if field.Bounds().Contains(ctx.LocalX, ctx.LocalY) {
				if field.FieldState().Disabled || field.FieldState().Type == FieldSeparator {
					return EventResult{Handled: true}
				}
				f.focused = index
				return f.handleField(index, f.fieldContext(ctx, field), evt)
			}
		}
		return EventResult{Handled: true}
	}
	if evt.IsKey() {
		key := evt.Key
		field := f.fields[f.focused]
		if key.Code == tea.KeyEsc && !field.SelectOpen() {
			return EventResult{Handled: true, Action: &Action{ID: "form.exit.backward"}, Invalidate: InvalidateFocus | InvalidatePaint}
		}
		if key.Code == tea.KeyTab && !field.SelectOpen() {
			if key.Mod.Contains(tea.ModShift) {
				return f.moveFocus(-1)
			}
			return f.moveFocus(1)
		}
		if (key.Code == tea.KeyUp || key.Code == tea.KeyDown) && !field.SelectOpen() {
			if key.Code == tea.KeyUp {
				return f.moveFocus(-1)
			}
			return f.moveFocus(1)
		}
	}
	return f.handleField(f.focused, f.fieldContext(ctx, f.fields[f.focused]), evt)
}

func (f *Form) handleField(index int, ctx EventContext, evt Event) EventResult {
	result := f.fields[index].Handle(ctx, evt)
	f.syncFields()
	if result.Action != nil {
		switch result.Action.ID {
		case "form.next":
			return f.moveFocus(1)
		}
	}
	return result
}

func (f *Form) syncFields() {
	for _, field := range f.fields {
		field.Sync()
	}
}

func (f *Form) fieldContext(ctx EventContext, field *FormField) EventContext {
	r := field.Bounds()
	ctx.LocalX -= r.X
	ctx.LocalY -= r.Y
	return ctx
}

func (f *Form) moveFocus(delta int) EventResult {
	for next := f.focused + delta; next >= 0 && next < len(f.fields); next += delta {
		field := f.fields[next].FieldState()
		if !field.Disabled && field.Type != FieldSeparator {
			f.focused = next
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
	}
	if delta < 0 {
		return EventResult{Handled: true, Action: &Action{ID: "form.exit.backward"}, Invalidate: InvalidateFocus | InvalidatePaint}
	}
	return EventResult{Handled: true, Action: &Action{ID: "form.exit.forward"}, Invalidate: InvalidateFocus | InvalidatePaint}
}

func (f *Form) FocusFirst() {
	for index, field := range f.fields {
		if !field.FieldState().Disabled && field.FieldState().Type != FieldSeparator {
			f.focused = index
			return
		}
	}
}
func (f *Form) FocusLast() {
	for index := len(f.fields) - 1; index >= 0; index-- {
		field := f.fields[index].FieldState()
		if !field.Disabled && field.Type != FieldSeparator {
			f.focused = index
			return
		}
	}
}

func (f *Form) color(role SemanticColor) cell.Color {
	if f.tokens == nil {
		return cell.NoColor
	}
	return f.tokens.Color(role)
}

func (f *Form) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: f.title, Description: f.detail}
}
