package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
)

// FormField is a Component that renders a single form field layout:
// [label] [control] on one row, with description and errors on subsequent rows.
type FormField struct {
	Base
	field   *FieldState
	tokens  *TokenSet
	control Component
	labelW  int

	selectOpen   bool
	selectItems  []FormOption
	selectCursor int
	selectList   *List
}

func NewFormTextField(id ComponentID, f *FieldState, tokens *TokenSet) *FormField {
	tf := NewTextField(id, tokens)
	tf.SetValue(f.Value.String())
	if f.Type == FieldPassword {
		tf.SetPassword(true)
	}
	if f.ReadOnly {
		tf.SetReadOnly(true)
	}
	if f.Disabled {
		tf.SetDisabled(true)
	}
	return &FormField{Base: NewBase(id), field: f, tokens: tokens, control: tf}
}

func NewFormSelectField(id ComponentID, f *FieldState, tokens *TokenSet) *FormField {
	list := NewList(id, tokens)
	items := make([]ListItem, len(f.Options))
	for i, o := range f.Options {
		items[i] = ListItem{ID: o.ID, Label: o.Label, Detail: o.Detail, Payload: o.ID}
	}
	sel := 0
	if v := f.Value.String(); v != "" {
		for i, o := range f.Options {
			if o.ID == v {
				sel = i
				break
			}
		}
	}
	list.SetItems(items)
	list.SelectIndex(sel)
	return &FormField{
		Base: NewBase(id), field: f, tokens: tokens, control: list,
		selectItems: f.Options, selectCursor: sel, selectList: list,
	}
}

func NewFormCycleField(id ComponentID, f *FieldState, tokens *TokenSet) *FormField {
	return &FormField{Base: NewBase(id), field: f, tokens: tokens, selectItems: f.Options}
}

func NewFormToggleField(id ComponentID, f *FieldState, tokens *TokenSet) *FormField {
	return &FormField{Base: NewBase(id), field: f, tokens: tokens}
}

func NewFormNavField(id ComponentID, f *FieldState, tokens *TokenSet) *FormField {
	return &FormField{Base: NewBase(id), field: f, tokens: tokens}
}

func NewFormSeparatorField(id ComponentID, f *FieldState, tokens *TokenSet) *FormField {
	return &FormField{Base: NewBase(id), field: f, tokens: tokens}
}

func NewFormField(id ComponentID, f *FieldState, tokens *TokenSet) *FormField {
	switch f.Type {
	case FieldText, FieldPassword:
		return NewFormTextField(id, f, tokens)
	case FieldSelect:
		return NewFormSelectField(id, f, tokens)
	case FieldCycle:
		return NewFormCycleField(id, f, tokens)
	case FieldToggle, FieldCheckbox:
		return NewFormToggleField(id, f, tokens)
	case FieldNav:
		return NewFormNavField(id, f, tokens)
	case FieldSeparator:
		return NewFormSeparatorField(id, f, tokens)
	default:
		return NewFormTextField(id, f, tokens)
	}
}

func (ff *FormField) SetTokens(ts *TokenSet) {
	ff.tokens = ts
	if t, ok := ff.control.(interface{ SetTokens(*TokenSet) }); ok {
		t.SetTokens(ts)
	}
}

func (ff *FormField) FieldState() *FieldState { return ff.field }

func (ff *FormField) SetLabelWidth(w int) { ff.labelW = w }

func (ff *FormField) Children() []Component {
	if ff.control != nil {
		return []Component{ff.control}
	}
	return nil
}

func (ff *FormField) Measure(_ LayoutContext, cs Constraints) Size {
	if ff.field.Type == FieldSeparator {
		return cs.ClampSize(Size{W: cs.MaxW, H: 1})
	}
	h := 1
	if ff.field.Description != "" {
		h++
	}
	if ff.field.HasErrors() {
		h += len(ff.field.Errors)
	}
	return cs.ClampSize(Size{W: cs.MaxW, H: h})
}

func (ff *FormField) Layout(ctx LayoutContext, r Rect) {
	ff.SetBounds(r)
	if ff.control == nil || ff.field.Type == FieldSeparator || ff.field.Type == FieldNav {
		return
	}
	lw := ff.labelW
	if lw == 0 {
		lw = 16
	}
	gap := 2
	cx := r.X + lw + gap
	cw := r.W - lw - gap
	if cw < 10 {
		cx = r.X + lw
		cw = r.W - lw
		gap = 0
	}
	ff.control.Layout(ctx, Rect{X: cx, Y: r.Y, W: cw, H: 1})
	_ = gap
}

func (ff *FormField) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := ff.Bounds()
	x := originX + r.X
	y := originY + r.Y
	// Background fill
	fillBG := cell.NewTrueColor(22, 27, 34) // default panel bg
	if ff.tokens != nil {
		fillBG = ff.tokens.Color(ColorPopupBg)
	}
	for row := 0; row < r.H; row++ {
		for col := 0; col < r.W; col++ {
			buf.Set(x+col, y+row, cell.BlankCell(fillBG))
		}
	}

	switch ff.field.Type {
	case FieldSeparator:
		ff.paintSeparator(buf, x, y, r.W, fillBG)
		return
	case FieldNav:
		ff.paintNav(buf, x, y, r.W, ctx, fillBG)
		return
	}

	lw := ff.labelW
	if lw == 0 {
		lw = 16
	}
	gap := 2
	labelFG := cell.NewTrueColor(174, 186, 204)
	if ff.field.ReadOnly || ff.field.Disabled {
		labelFG = cell.NewTrueColor(110, 118, 129)
	}

	// Label
	label := ff.field.Label
	if ff.field.Required && label != "" {
		label = label + "*"
	}
	runes := []rune(label)
	if len(runes) > lw {
		runes = runes[:lw]
	}
	buf.WriteString(x, y, string(runes), labelFG, fillBG, 0)
	// Fill remaining label column
	for j := len(runes); j < lw && j < r.W; j++ {
		buf.Set(x+j, y, cell.BlankCell(fillBG))
	}
	// Gap
	for j := lw; j < lw+gap && j < r.W; j++ {
		buf.Set(x+j, y, cell.BlankCell(fillBG))
	}

	// Control
	if ff.control != nil {
		ff.control.Paint(buf, originX, originY, ctx)
	}

	// Description + errors
	descY := y + 1
	if ff.field.Description != "" {
		descFG := cell.NewTrueColor(110, 118, 129)
		buf.WriteString(x+lw+gap, descY, ff.field.Description, descFG, fillBG, 0)
		descY++
	}
	for ei, err := range ff.field.Errors {
		errY := descY + ei
		if errY >= y+r.H {
			break
		}
		errFG := cell.NewTrueColor(248, 113, 113)
		buf.WriteString(x+lw+gap, errY, err, errFG, fillBG, 0)
	}
}

func (ff *FormField) Handle(ctx EventContext, msg tea.Msg) EventResult {
	if ff.field.Disabled {
		return EventResult{}
	}
	switch ff.field.Type {
	case FieldSelect:
		return ff.handleSelect(ctx, msg)
	case FieldCycle:
		return ff.handleCycle(ctx, msg)
	case FieldToggle, FieldCheckbox:
		return ff.handleToggle(ctx, msg)
	case FieldNav:
		return ff.handleNav(ctx, msg)
	default:
		if ff.control != nil {
			return ff.control.Handle(ff.childContext(ctx), msg)
		}
	}
	return EventResult{}
}

func (ff *FormField) handleSelect(ctx EventContext, msg tea.Msg) EventResult {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.Code {
		case tea.KeyEnter, tea.KeySpace:
			ff.selectOpen = !ff.selectOpen
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyUp:
			if ff.selectOpen && ff.selectCursor > 0 {
				ff.selectCursor--
				if ff.selectList != nil {
					ff.selectList.SelectIndex(ff.selectCursor)
				}
				return EventResult{Handled: true, Invalidate: InvalidatePaint}
			}
		case tea.KeyDown:
			if ff.selectOpen && ff.selectCursor < len(ff.selectItems)-1 {
				ff.selectCursor++
				if ff.selectList != nil {
					ff.selectList.SelectIndex(ff.selectCursor)
				}
				return EventResult{Handled: true, Invalidate: InvalidatePaint}
			}
		}
		if ff.selectOpen && ff.selectList != nil {
			return ff.selectList.Handle(ctx, msg)
		}
	}
	return EventResult{}
}

func (ff *FormField) handleCycle(ctx EventContext, msg tea.Msg) EventResult {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.Code {
		case tea.KeyLeft:
			if len(ff.selectItems) > 0 {
				idx := ff.selectedIndex() - 1
				if idx < 0 {
					idx = len(ff.selectItems) - 1
				}
				ff.field.Value = StringValue(ff.selectItems[idx].ID)
				ff.field.Dirty = !ff.field.Value.Equal(ff.field.Initial)
				ff.field.Validate()
				return EventResult{Handled: true, Invalidate: InvalidatePaint}
			}
		case tea.KeyRight:
			if len(ff.selectItems) > 0 {
				idx := ff.selectedIndex() + 1
				if idx >= len(ff.selectItems) {
					idx = 0
				}
				ff.field.Value = StringValue(ff.selectItems[idx].ID)
				ff.field.Dirty = !ff.field.Value.Equal(ff.field.Initial)
				ff.field.Validate()
				return EventResult{Handled: true, Invalidate: InvalidatePaint}
			}
		}
	}
	return EventResult{}
}

func (ff *FormField) handleToggle(ctx EventContext, msg tea.Msg) EventResult {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.Code == tea.KeyEnter || msg.Code == tea.KeySpace {
			ff.field.Value = BoolValue(!ff.field.Value.Bool())
			ff.field.Dirty = !ff.field.Value.Equal(ff.field.Initial)
			ff.field.Validate()
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
	}
	return EventResult{}
}

func (ff *FormField) handleNav(ctx EventContext, msg tea.Msg) EventResult {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.Code == tea.KeyEnter || msg.Code == tea.KeySpace {
			return EventResult{
				Handled: true,
				Action:  &Action{ID: ff.field.Target, Payload: ff.field.ID},
			}
		}
	}
	return EventResult{}
}

func (ff *FormField) selectedIndex() int {
	v := ff.field.Value.String()
	for i, o := range ff.selectItems {
		if o.ID == v {
			return i
		}
	}
	return 0
}

func (ff *FormField) childContext(ctx EventContext) EventContext {
	br := ff.Bounds()
	if ff.control != nil {
		cr := ff.control.Bounds()
		return EventContext{
			Theme:   ctx.Theme,
			Focused: ctx.Focused && cr.Contains(ctx.LocalX, ctx.LocalY),
			LocalX:  ctx.LocalX - cr.X,
			LocalY:  ctx.LocalY - cr.Y,
			ScreenX: ctx.ScreenX,
			ScreenY: ctx.ScreenY,
		}
	}
	return EventContext{
		Theme:   ctx.Theme,
		Focused: ctx.Focused && br.Contains(ctx.LocalX, ctx.LocalY),
		LocalX:  ctx.LocalX - br.X,
		LocalY:  ctx.LocalY - br.Y,
		ScreenX: ctx.ScreenX,
		ScreenY: ctx.ScreenY,
	}
}

func (ff *FormField) paintSeparator(buf *surface.CellBuffer, x, y, w int, bg cell.Color) {
	for i := 0; i < w; i++ {
		buf.Set(x+i, y, cell.Cell{Char: '─', Width: 1, FG: cell.NewTrueColor(48, 54, 61), BG: bg})
	}
}

func (ff *FormField) paintNav(buf *surface.CellBuffer, x, y, w int, ctx PaintContext, bg cell.Color) {
	text := ff.field.Label
	if ff.field.Description != "" {
		text += "  " + ff.field.Description
	}
	fg := cell.NewTrueColor(88, 166, 255)
	if ff.field.Disabled {
		fg = cell.NewTrueColor(110, 118, 129)
	}
	buf.WriteString(x, y, text, fg, bg, 0)
	// Arrow indicator
	arrow := "  >"
	buf.WriteString(x+w-len([]rune(arrow)), y, arrow, cell.NewTrueColor(110, 118, 129), bg, 0)
}
