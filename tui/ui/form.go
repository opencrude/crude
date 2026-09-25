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
	model            *FormModel
	title            string
	detail           string
	tokens           *TokenSet
	fields           []*FormField
	focused          int
	hovered          int
	fieldGap         int
	scroll           *ScrollModel
	arrowExits       bool
	compact          bool
	hideDescriptions bool
	surfaceRole      SemanticColor
	surfaceSet       bool
	ruleFullBleed    bool
	ruleBleedX       int
	ruleBleedW       int
	pointerField     int
}

func NewForm(id ComponentID, model *FormModel, title, detail string, tokens *TokenSet) *Form {
	f := &Form{Base: NewBase(id), model: model, title: title, detail: detail, tokens: tokens, focused: -1, hovered: -1, pointerField: -1, scroll: NewScrollModel()}
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
	f.SetRuleFullBleed(true)
	return f
}

// SetRuleFullBleed configures whether the form's separator rules (header
// rule and FieldSeparator rows) may expand beyond the form's content width
// when the parent supplies a wider bleed row. Docked forms opt in so their
// rules span the whole deck row edge to edge while fields stay in the
// padded column; floating settings keep rules bounded to the popup.
// Per-separator control stays with the form; the parent only supplies the
// row geometry.
func (f *Form) SetRuleFullBleed(v bool) { f.ruleFullBleed = v }

// SetRuleBleed is called by the parent (PanelDeck) at layout time with the
// deck row geometry a full-bleed rule may span: offsetX columns left of the
// form's own origin, width the total row width. Width 0 keeps rules bounded
// to the form's own width.
func (f *Form) SetRuleBleed(offsetX, width int) { f.ruleBleedX, f.ruleBleedW = offsetX, width }

// ruleSpan resolves where separator rules paint relative to the form
// origin: their own column unless full-bleed rules are enabled and the
// parent published a wider row (the usual case for a padded deck).
func (f *Form) ruleSpan(contentW int) (x, w int) {
	if f.ruleFullBleed && f.ruleBleedW-f.ruleBleedX > contentW {
		return -f.ruleBleedX, f.ruleBleedW
	}
	return 0, contentW
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

// SetHideDescriptions omits cramped explanatory rows while retaining field
// content, validation, textarea height, and semantic help for keyboard users.
func (f *Form) SetHideDescriptions(v bool) {
	if f.hideDescriptions == v {
		return
	}
	f.hideDescriptions = v
	for _, field := range f.fields {
		field.SetHideDescription(v)
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
	if f.pointerField >= 0 && f.pointerField < len(f.fields) {
		f.fields[f.pointerField].CancelPointerCapture()
	}
	f.pointerField = -1
	f.hovered = -1
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
	if f.hideDescriptions {
		for _, field := range f.fields {
			field.SetHideDescription(true)
		}
	}
}

// Children is intentionally empty. Form retains its fields so a single focus
// scope controls field traversal and action routing.
func (f *Form) Children() []Component { return nil }

// TextControlAt returns the field owning the text control at a form-local
// point (TextControlAtProvider). Fields are hidden from host hit-testing, so
// context menus ask the form instead of guessing.
func (f *Form) TextControlAt(x, y int) Component {
	for _, field := range f.fields {
		if field == nil || !field.Bounds().Contains(x, y) {
			continue
		}
		if target := TextClipboardTargetIn(field); target != nil {
			return field
		}
	}
	return nil
}

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
	// Clamp against the final viewport before publishing field bounds. Doing
	// this after layout leaves the first resized frame at an obsolete offset.
	f.scroll.SetContent(f.contentHeight())
	f.scroll.SetViewport(r.H)
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
		fieldX, fieldW := 0, r.W
		if field.FieldState().Type == FieldSeparator {
			fieldX, fieldW = f.ruleSpan(r.W)
		}
		field.Layout(ctx, Rect{X: fieldX, Y: contentY - f.scroll.Offset(), W: fieldW, H: h})
		contentY += h
		if i < len(f.fields)-1 && f.fieldGap > 0 && field.FieldState().Type != FieldSeparator && f.fields[i+1].FieldState().Type != FieldSeparator {
			contentY += f.fieldGap
		}
	}
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
	ruleX, ruleW := f.ruleSpan(r.W)
	// Paint into a viewport-sized buffer so variable-height fields that
	// straddle the scroll edge cannot leak into chrome (footer actions,
	// window title, sibling panels). CellBuffer.Set already clips OOB writes.
	// The buffer reserves the rule's left spill (off columns) so full-bleed
	// separator rules can run the parent row edge to edge while field
	// content stays in the form's own padded column; the blit origin shifts
	// by the same spill.
	off := -min(0, ruleX)
	inner := surface.NewCellBuffer(off+max(1, max(r.W, ruleW+ruleX)), max(1, r.H))
	inner.Fill(cell.BlankCell(bg))
	if f.title != "" {
		inner.WriteString(off, 0, rendering.TruncateVisual(f.title, r.W, ""), fg, bg, cell.AttrBold)
		ruleY := 1
		if !f.compact || strings.TrimSpace(f.detail) != "" {
			inner.WriteString(off, 1, rendering.TruncateVisual(f.detail, r.W, ""), muted, bg, 0)
			ruleY = 2
		}
		inner.WriteString(ruleX+off, ruleY, strings.Repeat(string(FirstRune(f.tokens.Glyphs().BorderH, '─')), max(0, ruleW)), divider, bg, 0)
		// No y advance: field bounds already carry the header offset from Layout.
	}
	for index, field := range f.fields {
		fr := field.Bounds()
		if fr.Y+fr.H <= 0 || fr.Y >= r.H {
			continue
		}
		fieldCtx := ctx
		fieldCtx.Focused = ctx.Focused && index == f.focused
		fieldCtx.Hovered = ctx.Hovered && index == f.hovered && !fieldCtx.Focused
		field.Paint(inner, off, 0, fieldCtx)
	}
	for index, field := range f.fields {
		if !field.SelectOpen() {
			continue
		}
		fieldCtx := ctx
		fieldCtx.Focused = ctx.Focused && index == f.focused
		field.PaintOverlay(inner, off, 0, fieldCtx, r)
	}
	surface.Blit(buf, originX+r.X-off, originY+r.Y, inner, nil)
}

func (f *Form) Handle(ctx EventContext, evt Event) EventResult {
	if len(f.fields) == 0 {
		return EventResult{}
	}
	if f.pointerField >= 0 && f.pointerField < len(f.fields) &&
		(evt.Kind == EventMouseMotion || evt.Kind == EventMouseRelease) {
		index := f.pointerField
		result := f.handleField(index, f.fieldContext(ctx, f.fields[index]), evt)
		if evt.Kind == EventMouseRelease {
			f.pointerField = -1
		}
		return result
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
	if evt.Kind == EventMouseMotion {
		// An open dropdown owns hover: motion feeds its option list.
		for index, field := range f.fields {
			if field.SelectOpen() {
				return f.handleField(index, f.fieldContext(ctx, field), evt)
			}
		}
		// Hover is visual only: typing and Enter still target the keyboard-
		// focused field. Decorative and disabled rows have no hover state.
		hovered := -1
		for index, field := range f.fields {
			if field.Bounds().Contains(ctx.LocalX, ctx.LocalY) && !field.FieldState().skipFocus() {
				hovered = index
				break
			}
		}
		if hovered != f.hovered {
			f.hovered = hovered
			return EventResult{Handled: hovered >= 0, Invalidate: InvalidatePaint}
		}
		return EventResult{Handled: hovered >= 0}
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
				result := f.handleField(index, f.fieldContext(ctx, field), evt)
				if result.Handled && fieldPointerSelecting(field) {
					f.pointerField = index
				}
				return result
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
		if (key.Code == tea.KeyUp || key.Code == tea.KeyDown) && !field.SelectOpen() && !field.MultiLine() {
			if key.Code == tea.KeyUp {
				return f.moveFocus(-1, f.arrowExits)
			}
			return f.moveFocus(1, f.arrowExits)
		}
		if (key.Code == tea.KeyPgUp || key.Code == tea.KeyPgDown) && !field.SelectOpen() && !field.MultiLine() && f.scroll != nil {
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

func fieldPointerSelecting(field *FormField) bool {
	if field == nil {
		return false
	}
	if control := field.Control(); control != nil && control.IsSelecting() {
		return true
	}
	if area := field.Area(); area != nil && area.IsSelecting() {
		return true
	}
	// A key/value table drags inside its open cell editor.
	if grid := field.Grid(); grid != nil {
		if editor := grid.ActiveEditor(); editor != nil && editor.IsSelecting() {
			return true
		}
	}
	return false
}

func (f *Form) CancelPointerCapture() {
	if f.pointerField >= 0 && f.pointerField < len(f.fields) {
		f.fields[f.pointerField].CancelPointerCapture()
	}
	f.pointerField = -1
}

func (f *Form) NeedsFrame() bool {
	if f.focused < 0 || f.focused >= len(f.fields) {
		return false
	}
	field := f.fields[f.focused]
	if field == nil {
		return false
	}
	if grid := field.Grid(); grid != nil {
		return grid.NeedsFrame()
	}
	if area := field.Area(); area != nil {
		return area.NeedsFrame()
	}
	if control := field.Control(); control != nil {
		return control.NeedsFrame()
	}
	return false
}

func (f *Form) handleField(index int, ctx EventContext, evt Event) EventResult {
	result := f.fields[index].Handle(ctx, evt)
	f.syncFields()
	if result.Action != nil {
		switch result.Action.ID {
		case ActionTextPaste:
			result.Action.Payload = ClipboardPasteTarget(&formPasteTarget{form: f, index: index})
		case "form.next":
			return f.moveFocus(1, f.arrowExits)
		}
	}
	return result
}

type formPasteTarget struct {
	form  *Form
	index int
}

func (t *formPasteTarget) PasteActive() bool {
	return t != nil && t.form != nil && t.index >= 0 && t.index < len(t.form.fields) && t.form.focused == t.index
}

func (t *formPasteTarget) PasteText(text string) {
	if !t.PasteActive() {
		return
	}
	t.form.handleField(t.index, EventContext{Focused: true}, CustomEvent(tea.PasteMsg{Content: text}))
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
			// The outgoing field drops any open cell editor: an unfocused
			// field must never keep a caret or hold unabsorbed text.
			if f.focused >= 0 && f.focused < len(f.fields) {
				f.fields[f.focused].Blur()
			}
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

// FocusField moves keyboard ownership to a retained field by semantic ID.
// Composite hosts can focus a target after replacing the form's model without
// depending on its row index or current scroll position.
func (f *Form) FocusField(id string) bool {
	for index, field := range f.fields {
		if field.FieldState().ID == id && !field.FieldState().skipFocus() {
			f.focused = index
			f.ensureFocusedVisible()
			return true
		}
	}
	return false
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
