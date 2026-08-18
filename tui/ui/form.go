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
	model       *FormModel
	title       string
	detail      string
	tokens      *TokenSet
	fields      []*FormField
	focused     int
	fieldGap    int
	scroll      *ScrollModel
	arrowExits  bool
	compact     bool
	surfaceRole SemanticColor
	surfaceSet  bool
}

func NewForm(id ComponentID, model *FormModel, title, detail string, tokens *TokenSet) *Form {
	f := &Form{Base: NewBase(id), model: model, title: title, detail: detail, tokens: tokens, focused: -1, scroll: NewScrollModel()}
	f.rebuild()
	return f
}

// NewDockedForm builds a form that shares the chat/shell surface color.
// Use this for PanelDeck content. Floating windows keep NewForm (popup fill).
func NewDockedForm(id ComponentID, model *FormModel, title, detail string, tokens *TokenSet) *Form {
	f := NewForm(id, model, title, detail, tokens)
	f.SetSurfaceBackground(ColorBg)
	f.SetCompact(true)
	f.SetArrowExitsForm(true)
	return f
}

// SetCompact enables the lean docked-form density: title+rule header, no
// inter-field gap, and single-line fields. Descriptions stay on the field
// for hints; they do not grow the dock. Floating settings keep the spacious
// default.
func (f *Form) SetCompact(v bool) {
	f.compact = v
	if v {
		f.fieldGap = 0
	}
	for _, field := range f.fields {
		field.SetCompact(v)
	}
}

// SetSurfaceBackground selects the form fill role. Docked session panels use
// ColorBg so they sit in the chat column; floating settings keep ColorPopupBg.
func (f *Form) SetSurfaceBackground(role SemanticColor) {
	f.surfaceRole = role
	f.surfaceSet = true
	for _, field := range f.fields {
		field.SetSurfaceBackground(role)
	}
}

func (f *Form) SetForm(model *FormModel, title, detail string) {
	sameModel := f.model == model && f.sameFieldStructure(model)
	f.model, f.title, f.detail = model, title, detail
	if sameModel {
		for _, field := range f.fields {
			field.Sync()
		}
	} else {
		f.rebuild()
	}
	// Rebuild replaces fields with empty bounds. Re-layout into the current
	// form rect so the next paint shows fields without waiting for a host
	// InvalidateLayout (category switches often only mark paint dirty).
	if !f.Bounds().Empty() {
		f.Layout(LayoutContext{}, f.Bounds())
	}
}

func (f *Form) sameFieldStructure(model *FormModel) bool {
	if model == nil || len(f.fields) != model.FieldCount() {
		return false
	}
	for index, state := range model.Fields() {
		if f.fields[index].FieldState().ID != state.ID {
			return false
		}
	}
	return true
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
	f.focused = -1
	for index, field := range f.fields {
		if field.FieldState().ID == focusedID {
			f.focused = index
			break
		}
	}
	if f.focused < 0 {
		f.FocusFirst()
	}
	if f.surfaceSet {
		for _, field := range f.fields {
			field.SetSurfaceBackground(f.surfaceRole)
		}
	}
	if f.compact {
		for _, field := range f.fields {
			field.SetCompact(true)
		}
	}
}

// Children is intentionally empty. Form retains its fields so a single focus
// scope controls field traversal and action routing.
func (f *Form) Children() []Component { return nil }

func (f *Form) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: f.PreferredHeight(cs.MaxH)})
}

// SetFieldGap configures the inter-field vertical spacing in rows. Use 0
// (default) for compact dialog panels; use 1 for settings forms.
func (f *Form) SetFieldGap(gap int) { f.fieldGap = gap }

// SetArrowExitsForm, when true, lets Up/Down at the first/last field leave
// the form (settings category rail). The default clamps so docked lists do
// not close or dump focus onto the action bar.
func (f *Form) SetArrowExitsForm(v bool) { f.arrowExits = v }

// Scroll returns the form's vertical scroll model for an on-demand scrollbar.
func (f *Form) Scroll() *ScrollModel { return f.scroll }

// PreferredHeight returns the rows required by the current field state,
// including validation messages and inter-field gaps. Open selects overlay
// and do not add to the layout height.
func (f *Form) PreferredHeight(maxHeight int) int {
	return min(maxHeight, f.contentHeight())
}

func (f *Form) headerHeight() int {
	if f.title == "" {
		return 0
	}
	if f.compact && strings.TrimSpace(f.detail) == "" {
		return 2
	}
	return 3
}

func (f *Form) contentHeight() int {
	height := f.headerHeight()
	for i, field := range f.fields {
		if i > 0 && field.FieldState().Type == FieldSeparator {
			height++
		}
		height += field.height()
		if i < len(f.fields)-1 && f.fieldGap > 0 && field.FieldState().Type != FieldSeparator && f.fields[i+1].FieldState().Type != FieldSeparator {
			height += f.fieldGap
		}
	}
	return height
}

func (f *Form) Layout(ctx LayoutContext, r Rect) {
	f.SetBounds(r)
	if f.scroll == nil {
		f.scroll = NewScrollModel()
	}
	y := f.headerHeight()
	labelW := 0
	for _, field := range f.fields {
		st := field.FieldState()
		if st.Type == FieldSeparator || st.Type == FieldSpacer {
			continue
		}
		labelW = max(labelW, rendering.VisibleWidth(st.Label))
	}
	if !f.compact {
		// Spacious forms append dirty/required markers after the label; they
		// borrow this extra column from the label→control gap.
		labelW++
	}
	labelW = min(labelW, max(8, r.W/2))
	colW := f.tableColumnWidths()
	contentY := y
	for i, field := range f.fields {
		if i > 0 && field.FieldState().Type == FieldSeparator {
			contentY++
		}
		field.SetLabelWidth(labelW)
		field.SetColumnWidths(colW)
		h := field.height()
		field.Layout(ctx, Rect{X: 0, Y: contentY - f.scroll.Offset(), W: r.W, H: h})
		contentY += h
		if i < len(f.fields)-1 && f.fieldGap > 0 && field.FieldState().Type != FieldSeparator && f.fields[i+1].FieldState().Type != FieldSeparator {
			contentY += f.fieldGap
		}
	}
	f.scroll.SetContent(contentY)
	f.scroll.SetViewport(r.H)
	for _, field := range f.fields {
		field.layoutOverlay(ctx, r.H)
	}
}

func (f *Form) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := f.Bounds()
	if r.Empty() {
		return
	}
	bg, fg, muted, divider := f.surfaceColor(), f.color(ColorFg), f.color(ColorMuted), f.color(ColorPopupDivider)
	// Paint into a viewport-sized buffer so variable-height fields that
	// straddle the scroll edge cannot leak into chrome (footer actions,
	// window title, sibling panels). CellBuffer.Set already clips OOB writes.
	inner := surface.NewCellBuffer(max(1, r.W), max(1, r.H))
	inner.Fill(cell.BlankCell(bg))
	if f.title != "" {
		inner.WriteString(0, 0, rendering.TruncateVisual(f.title, r.W, ""), fg, bg, cell.AttrBold)
		ruleY := 1
		if !f.compact || strings.TrimSpace(f.detail) != "" {
			inner.WriteString(0, 1, rendering.TruncateVisual(f.detail, r.W, ""), muted, bg, 0)
			ruleY = 2
		}
		inner.WriteString(0, ruleY, strings.Repeat(string(FirstRune(f.tokens.Glyphs().BorderH, '─')), max(0, r.W)), divider, bg, 0)
		// No y advance: field bounds already carry the header offset from Layout.
	}
	for index, field := range f.fields {
		fr := field.Bounds()
		if fr.Y+fr.H <= 0 || fr.Y >= r.H {
			continue
		}
		fieldCtx := ctx
		fieldCtx.Focused = ctx.Focused && index == f.focused
		field.Paint(inner, 0, 0, fieldCtx)
	}
	for index, field := range f.fields {
		if !field.SelectOpen() {
			continue
		}
		fieldCtx := ctx
		fieldCtx.Focused = ctx.Focused && index == f.focused
		field.PaintOverlay(inner, 0, 0, fieldCtx, r)
	}
	surface.Blit(buf, originX+r.X, originY+r.Y, inner, nil)
}

func (f *Form) Handle(ctx EventContext, evt Event) EventResult {
	if len(f.fields) == 0 {
		return EventResult{}
	}
	if evt.Kind == EventMouseWheel {
		for index, field := range f.fields {
			if field.SelectOpen() {
				return f.handleField(index, f.fieldContext(ctx, field), evt)
			}
		}
		if f.scroll != nil && f.scroll.MaxOffset() > 0 {
			switch evt.MouseWheel.Button {
			case tea.MouseWheelUp:
				f.scroll.ScrollBy(-3)
			case tea.MouseWheelDown:
				f.scroll.ScrollBy(3)
			}
			f.Layout(LayoutContext{}, f.Bounds())
			return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
		}
	}
	if evt.Kind == EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		// An open dropdown swallows clicks outside its panel: cancel it
		// without moving focus or acting on whatever was underneath.
		for index, field := range f.fields {
			if !field.SelectOpen() {
				continue
			}
			fr := field.Bounds()
			if !field.selectPanelContains(ctx.LocalX-fr.X, ctx.LocalY-fr.Y) {
				field.closeSelect()
				return EventResult{Handled: true, Invalidate: InvalidatePaint | InvalidateLayout}
			}
			f.focused = index
			return f.handleField(index, f.fieldContext(ctx, field), evt)
		}
		for index, field := range f.fields {
			if field.Bounds().Contains(ctx.LocalX, ctx.LocalY) {
				if field.FieldState().skipFocus() {
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
			return EventResult{Handled: true, Action: &Action{ID: "form.cancel"}, Invalidate: InvalidateFocus | InvalidatePaint}
		}
		if key.Code == tea.KeyTab && !field.SelectOpen() {
			if key.Mod.Contains(tea.ModShift) {
				return f.moveFocus(-1, true)
			}
			return f.moveFocus(1, true)
		}
		if (key.Code == tea.KeyUp || key.Code == tea.KeyDown) && !field.SelectOpen() {
			if key.Code == tea.KeyUp {
				return f.moveFocus(-1, f.arrowExits)
			}
			return f.moveFocus(1, f.arrowExits)
		}
		if (key.Code == tea.KeyPgUp || key.Code == tea.KeyPgDown) && !field.SelectOpen() && f.scroll != nil {
			delta := f.scroll.Viewport() - 1
			if delta < 1 {
				delta = 1
			}
			if key.Code == tea.KeyPgUp {
				f.scroll.ScrollBy(-delta)
			} else {
				f.scroll.ScrollBy(delta)
			}
			f.Layout(LayoutContext{}, f.Bounds())
			return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
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
			return f.moveFocus(1, f.arrowExits)
		}
	}
	return result
}

func (f *Form) syncFields() {
	for _, field := range f.fields {
		field.Sync()
	}
}

// CloseOpenSelects cancels every open dropdown without committing. Screens
// call this when interaction lands outside the form (rail, search, chrome)
// so no orphaned dropdown survives a focus change elsewhere.
func (f *Form) CloseOpenSelects() bool {
	closed := false
	for _, field := range f.fields {
		if field.SelectOpen() {
			field.closeSelect()
			closed = true
		}
	}
	return closed
}

func (f *Form) fieldContext(ctx EventContext, field *FormField) EventContext {
	r := field.Bounds()
	ctx.LocalX -= r.X
	ctx.LocalY -= r.Y
	return ctx
}

func (f *Form) moveFocus(delta int, exit bool) EventResult {
	for next := f.focused + delta; next >= 0 && next < len(f.fields); next += delta {
		field := f.fields[next].FieldState()
		if !field.skipFocus() {
			f.focused = next
			f.ensureFocusedVisible()
			return EventResult{Handled: true, Invalidate: InvalidatePaint | InvalidateLayout}
		}
	}
	if !exit {
		return EventResult{Handled: true}
	}
	if delta < 0 {
		return EventResult{Handled: true, Action: &Action{ID: "form.exit.backward"}, Invalidate: InvalidateFocus | InvalidatePaint}
	}
	return EventResult{Handled: true, Action: &Action{ID: "form.exit.forward"}, Invalidate: InvalidateFocus | InvalidatePaint}
}

func (f *Form) FocusFirst() {
	for index, field := range f.fields {
		if !field.FieldState().skipFocus() {
			f.focused = index
			return
		}
	}
}
func (f *Form) FocusLast() {
	for index := len(f.fields) - 1; index >= 0; index-- {
		if !f.fields[index].FieldState().skipFocus() {
			f.focused = index
			return
		}
	}
}

func (f *Form) tableColumnWidths() []int {
	raw := make([][]string, 0, len(f.fields))
	inherited := make([]bool, 0, len(f.fields))
	maxN := 0
	for _, field := range f.fields {
		cols := field.compactColumnsRaw()
		raw = append(raw, cols)
		st := field.FieldState()
		inherited = append(inherited, st != nil && st.Inherited)
		maxN = max(maxN, len(cols))
	}
	widths := make([]int, maxN)
	for i, cols := range raw {
		for j, col := range padInheritedColumns(cols, inherited[i], maxN) {
			widths[j] = max(widths[j], rendering.VisibleWidth(col))
		}
	}
	return widths
}

func (f *Form) ensureFocusedVisible() {
	if f.scroll == nil || f.focused < 0 || f.focused >= len(f.fields) {
		return
	}
	field := f.fields[f.focused]
	start := field.Bounds().Y + f.scroll.Offset()
	end := start + max(1, field.Bounds().H) - 1
	off := f.scroll.Offset()
	vh := f.scroll.Viewport()
	if start < off {
		f.scroll.ScrollTo(start)
	} else if end >= off+vh {
		f.scroll.ScrollTo(end - vh + 1)
	}
	if !f.Bounds().Empty() {
		f.Layout(LayoutContext{}, f.Bounds())
	}
}

func (f *Form) color(role SemanticColor) cell.Color {
	if f.tokens == nil {
		return cell.NoColor
	}
	return f.tokens.Color(role)
}

func (f *Form) surfaceColor() cell.Color {
	if f.surfaceSet {
		return f.color(f.surfaceRole)
	}
	return f.color(ColorPopupBg)
}

func (f *Form) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: f.title, Description: f.detail}
}
