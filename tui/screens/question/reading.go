package question

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

// answerList is the question's reading and choice surface. The shared
// VirtualList owns variable-height hit testing, selection and the only scroll
// model; the scrollbar manipulates that same model. Wrapped lines are derived
// only when their source or width changes, never during paint.
type answerList struct {
	*ui.VirtualList
	bar    *ui.ScrollbarControl
	items  []ui.ListItem
	width  int
	dirty  bool
	reset  bool
	offset int
}

type wrappedAnswer struct {
	source    ui.ListItem
	lines     []string
	labelRows int
	indent    int
}

func newAnswerList(id ui.ComponentID, tokens *ui.TokenSet) *answerList {
	list := ui.NewVirtualList(id, tokens)
	list.SetSelectionAnchorsScroll(false)
	list.SetOpaqueRowFill(false)
	list.SetRowPainter(paintAnswerRow)
	a := &answerList{VirtualList: list, width: -1, dirty: true}
	a.bar = ui.NewScrollbarControl(ui.NewID(id, "scrollbar"), list.Scroll(), tokens)
	a.bar.SetPointerTargetSize(1)
	return a
}

func (a *answerList) SetTokens(tokens *ui.TokenSet) {
	a.VirtualList.SetTokens(tokens)
	a.bar.SetTokens(tokens)
}

func (a *answerList) SetActivateAction(action string) {
	a.VirtualList.SetActivateAction(action)
}

func (a *answerList) Items() []ui.ListItem { return a.items }

func (a *answerList) SetItems(items []ui.ListItem) {
	selected, _ := a.Selected()
	if !a.dirty {
		a.offset = a.Scroll().Offset()
	}
	a.items = items
	a.dirty = true
	// Install the small source collection before geometry exists. Selection
	// intents must not force a whole long question to wrap at one column.
	projected := make([]ui.VirtItem, len(items))
	for index, item := range items {
		projected[index] = ui.VirtItem{ID: item.ID, Height: 1, Label: item.Label, Disabled: !item.Selectable(), Payload: wrappedAnswer{source: item}}
	}
	a.VirtualList.SetItems(projected)
	for index, item := range items {
		if item.ID == selected.ID && item.Selectable() {
			a.VirtualList.SelectIndex(index)
			break
		}
	}
}

func (a *answerList) Selected() (ui.ListItem, bool) {
	item, ok := a.VirtualList.Selected()
	if !ok {
		return ui.ListItem{}, false
	}
	row, ok := item.Payload.(wrappedAnswer)
	return row.source, ok
}

func (a *answerList) SelectIndex(index int) bool {
	return a.VirtualList.SelectIndex(index)
}

func (a *answerList) ContentHeight() int {
	if a.dirty {
		// Before first layout claim enough room to read, then converge using
		// actual parent geometry. Never invent a wrap-width floor.
		return 16
	}
	return a.Scroll().Content()
}

func (a *answerList) ResetViewport() { a.reset = true }

func (a *answerList) reflow(width int) {
	if !a.dirty && a.width == width {
		return
	}
	selected, _ := a.Selected()
	items := make([]ui.VirtItem, len(a.items))
	for index, item := range a.items {
		indent := 0
		if item.Icon != "" {
			indent = min(max(0, width-1), rendering.VisibleWidth(item.Icon)+1)
		}
		row := wrappedAnswer{source: item, indent: indent}
		if !item.Separator {
			row.lines = wrapAnswer(item.Label, max(1, width-indent))
			row.labelRows = len(row.lines)
			if item.Detail != "" {
				row.lines = append(row.lines, wrapAnswer(item.Detail, max(1, width-indent))...)
			}
		}
		items[index] = ui.VirtItem{ID: item.ID, Height: max(1, len(row.lines)), Label: item.Label, Disabled: !item.Selectable(), Payload: row}
	}
	a.VirtualList.SetItems(items)
	if a.dirty {
		a.Scroll().ScrollTo(a.offset)
	}
	for index, item := range a.items {
		if item.ID == selected.ID && item.Selectable() {
			a.VirtualList.SelectIndex(index)
			break
		}
	}
	a.width, a.dirty = width, false
}

// Split logical lines before wrapping; WordWrap measures terminal columns,
// whereas line breaks are source structure. Display sanitation never mutates
// the original question, option, or submitted answer.
func wrapAnswer(text string, width int) []string {
	text = rendering.ExpandTabs(rendering.SanitizeForTerminal(text), 4)
	var rows []string
	for _, line := range strings.Split(text, "\n") {
		rows = append(rows, rendering.WordWrap(line, width)...)
	}
	return rows
}

func (a *answerList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	a.SetBounds(r)
	width := max(0, r.W-1)
	a.reflow(max(1, width))
	// The wrapper receives zero-origin bounds from focusGate. Both controls
	// use that same parent-local geometry and reserve a stable gutter.
	a.VirtualList.Layout(ctx, ui.Rect{W: width, H: r.H})
	a.bar.Layout(ctx, ui.Rect{X: width, W: min(1, r.W), H: r.H})
	if a.reset {
		a.Scroll().SetAutoScroll(false)
		a.Scroll().ScrollTo(0)
		a.reset = false
	}
}

func (a *answerList) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	a.VirtualList.Paint(buf, ox, oy, ctx)
	a.bar.Paint(buf, ox, oy, ctx)
}

func (a *answerList) CancelPointerCapture() { a.bar.CancelPointerCapture() }

func (a *answerList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if a.bar.IsDragging() || (evt.IsMouse() && a.bar.Bounds().Contains(ctx.LocalX, ctx.LocalY)) {
		return a.bar.Handle(ui.ChildEventContext(ctx, a.bar.Bounds()), evt)
	}
	if evt.IsKey() {
		scroll := a.Scroll()
		switch evt.Key.Code {
		case tea.KeyPgUp:
			scroll.ScrollBy(-max(1, scroll.Viewport()-1))
		case tea.KeyPgDown:
			scroll.ScrollBy(max(1, scroll.Viewport()-1))
		case tea.KeyHome:
			scroll.ScrollTo(0)
		case tea.KeyEnd:
			scroll.ScrollTo(scroll.MaxOffset())
		case tea.KeySpace:
			evt = ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter})
		default:
			goto delegate
		}
		if evt.Key.Code != tea.KeyEnter {
			scroll.SetAutoScroll(false)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
delegate:
	result := a.VirtualList.Handle(ctx, evt)
	if evt.IsKey() && (evt.Key.Code == tea.KeyUp || evt.Key.Code == tea.KeyDown) {
		a.revealSelectionStart()
	}
	if result.Action != nil {
		if item, ok := result.Action.Payload.(ui.VirtItem); ok {
			if row, ok := item.Payload.(wrappedAnswer); ok {
				result.Action.Payload = row.source
			}
		}
	}
	return result
}

func (a *answerList) revealSelectionStart() {
	index := a.SelectedIndex()
	if index >= 0 {
		// An option can exceed the viewport. Show its beginning, rather than
		// EnsureSelectedVisible's end alignment for a whole oversized item.
		start := a.ItemStartRow(index)
		scroll := a.Scroll()
		if start < scroll.Offset() || start >= scroll.Offset()+scroll.Viewport() {
			scroll.ScrollTo(start)
		}
	}
}

func paintAnswerRow(buf *surface.CellBuffer, x, y, width int, item ui.VirtItem, localRow int, state ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
	row, ok := item.Payload.(wrappedAnswer)
	if !ok || tokens == nil || width <= 0 {
		return
	}
	fg, bg := tokens.Color(ui.ColorFg), tokens.Color(ui.ColorBg)
	if row.source.Selectable() && state != ui.StateDefault && state != ui.StateDisabled && (row.source.Detail == "" || localRow < row.labelRows) {
		fg, bg = tokens.ListRowColors(state)
	}
	ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	if row.source.Separator {
		glyph := ui.FirstRune(tokens.Glyphs().BorderH, '─')
		buf.WriteString(x, y, strings.Repeat(string(glyph), width), tokens.Color(ui.ColorPopupDivider), bg, 0)
		return
	}
	if localRow >= len(row.lines) {
		return
	}
	attr := cell.Attrs(0)
	if row.source.Header || (localRow < row.labelRows && (row.source.Detail != "" || state == ui.StateFocused || state == ui.StateSelected)) {
		attr = cell.AttrBold
	}
	if localRow == 0 && row.indent > 0 {
		buf.WriteString(x, y, rendering.TruncateVisual(row.source.Icon, row.indent, ""), fg, bg, attr)
	}
	buf.WriteString(x+row.indent, y, rendering.TruncateVisual(row.lines[localRow], max(0, width-row.indent), ""), fg, bg, attr)
}
