package launcher

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

type searchWrapper struct {
	ui.Base
	owner *Screen
	field *ui.FilterField
}

func (s *searchWrapper) SetTokens(tokens *ui.TokenSet) {
	s.field.SetTokens(tokens)
	s.field.SetBackgroundColor(tokens.Color(ui.ColorSubPaneBg))
	s.field.SetPromptColor(tokens.Color(ui.ColorMuted))
}
func (s *searchWrapper) TextEditor() ui.TextClipboardTarget { return s.field.TextEditor() }
func (s *searchWrapper) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (s *searchWrapper) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	s.field.Paint(buf, ox+s.Bounds().X, oy+s.Bounds().Y, ctx)
}
func (s *searchWrapper) Semantics() ui.Semantics { return s.field.Semantics() }
func (s *searchWrapper) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.Kind == ui.EventMouseWheel {
		return s.forwardWheel(evt.MouseWheel)
	}
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		s.owner.filterOn = true
		s.owner.focusID(SearchID)
		if s.owner.selected < 2 && len(s.owner.filtered) > 0 {
			s.owner.selected = s.owner.filtered[0] + 2
			s.owner.syncSelection()
		}
	}
	if evt.IsKey() {
		if evt.Key.Code == tea.KeyEsc {
			if _, selected := s.field.TextEditor().TextSelection(); selected {
				s.field.TextEditor().ClearTextSelection()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
			}
		}
		switch evt.Key.Code {
		case tea.KeyUp, tea.KeyDown, tea.KeyEnter, tea.KeyEsc, tea.KeyTab:
			return s.owner.handleKey(evt.Key)
		case tea.KeyBackspace:
			if s.field.Value() == "" {
				s.owner.deactivateFilter()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus}
			}
		}
	}
	before := s.field.Value()
	res := s.field.Handle(ctx, evt)
	if before != s.field.Value() {
		s.owner.filterOn = true
		s.owner.applyFilter()
		res.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	return res
}
func (s *searchWrapper) forwardWheel(m tea.MouseWheelMsg) ui.EventResult {
	switch m.Button {
	case tea.MouseWheelUp:
		s.owner.list.list.Scroll().ScrollBy(-3)
	case tea.MouseWheelDown:
		s.owner.list.list.Scroll().ScrollBy(3)
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

const (
	workspaceContentHeight = 2
	workspaceRowStride     = workspaceContentHeight + 1
)

// workspaceList virtualizes name/path pairs with a quiet, non-interactive row
// between resources. Painting, scrolling and pointer targeting share the stride.
type workspaceList struct {
	ui.Base
	owner   *Screen
	list    *ui.VirtualList
	hovered int // workspace index
}

func (l *workspaceList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	l.SetBounds(r)
	l.list.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (l *workspaceList) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	l.list.Paint(buf, ox+l.Bounds().X, oy+l.Bounds().Y, ctx)
}
func (l *workspaceList) indexAt(x, y int) int {
	b := l.Bounds()
	if x < 0 || x >= b.W || y < 0 || y >= b.H {
		return -1
	}
	absRow := l.list.Scroll().Offset() + y
	row := l.list.ItemIndexAtRow(absRow)
	if row < 0 || row >= len(l.owner.filtered) || absRow-row*workspaceRowStride >= workspaceContentHeight {
		return -1
	}
	return l.owner.filtered[row]
}
func (l *workspaceList) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() {
		l.owner.focused = WorkspaceListID
		result := l.owner.handleKey(event.Key)
		if result.Handled || !l.owner.filterOn {
			return result
		}
		l.owner.focusID(SearchID)
		ctx.Focused = true
		return l.owner.search.Handle(ctx, event)
	}
	if event.Kind == ui.EventCustom && l.owner.filterOn {
		l.owner.focusID(SearchID)
		ctx.Focused = true
		return l.owner.search.Handle(ctx, event)
	}
	switch event.Kind {
	case ui.EventMouseMotion:
		index := l.indexAt(ctx.LocalX, ctx.LocalY)
		if l.hovered != index {
			l.hovered = index
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case ui.EventMouseClick:
		if event.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		index := l.indexAt(ctx.LocalX, ctx.LocalY)
		if index < 0 {
			return ui.EventResult{}
		}
		l.owner.selectFlat(index + 2)
		l.owner.focusID(WorkspaceListID)
		return ui.EventResult{Handled: true, Cmd: l.owner.activateSelected(), Invalidate: ui.InvalidatePaint}
	case ui.EventMouseWheel:
		return l.list.Handle(ctx, event)
	}
	return ui.EventResult{}
}
func (l *workspaceList) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleList, Label: "Recent workspaces"}
}
func (l *workspaceList) rowPainter(buf *surface.CellBuffer, x, y, width int, item ui.VirtItem, row int, _ ui.ControlState, ctx ui.PaintContext, tokens *ui.TokenSet) {
	index, ok := item.Payload.(int)
	if !ok {
		return
	}
	if row >= workspaceContentHeight {
		ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(tokens.Color(ui.ColorBg)))
		return
	}
	l.paintEntry(buf, x, y, width, row, index, ctx, tokens)
}
func (l *workspaceList) paintEntry(buf *surface.CellBuffer, x, y, width, row, index int, ctx ui.PaintContext, tokens *ui.TokenSet) {
	if width <= 0 {
		return
	}
	ws := l.owner.workspaces[index]
	fg, meta, bg := tokens.Color(ui.ColorFg), tokens.Color(ui.ColorMuted), tokens.Color(ui.ColorBg)
	if l.owner.selected == index+2 {
		bg = tokens.Color(ui.ColorSelection)
		if ctx.Focused || l.owner.focused == SearchID {
			fg = tokens.Color(ui.ColorAccent)
		}
	} else if ctx.Hovered && l.hovered == index {
		bg = tokens.Color(ui.ColorHover)
	}
	ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	icons := filetree.NewIconResolver()
	if th := tokens.Theme(); th != nil {
		icons.ApplyTheme(&th.Colors.Tree)
	}
	icon := icons.ResolveIcon(ws.Name, true, false, false)
	prefix := icon.Icon + " "
	prefixW := rendering.VisibleWidth(prefix)
	if prefixW > width {
		prefix, prefixW = "", 0
	}
	if row == 1 {
		buf.WriteString(x+prefixW, y, l.owner.displayPath(ws.Path, width-prefixW), meta, bg, 0)
		return
	}
	count := ""
	if ws.SessionCount == 1 {
		count = "1 session"
	} else if ws.SessionCount > 1 {
		count = fmt.Sprintf("%d sessions", ws.SessionCount)
	}
	available := width - prefixW
	if rendering.VisibleWidth(ws.Name)+rendering.VisibleWidth(count)+2 > available {
		count = ""
	}
	nameW := available
	if count != "" {
		nameW -= rendering.VisibleWidth(count) + 2
	}
	ink := rendering.ThemeColorToCell(icon.Color)
	if ink.IsDefault() {
		ink = meta
	}
	buf.WriteString(x, y, prefix, ink, bg, 0)
	buf.WriteString(x+prefixW, y, rendering.TruncateVisual(ws.Name, max(0, nameW), "…"), fg, bg, cell.AttrBold)
	buf.WriteString(x+width-rendering.VisibleWidth(count), y, count, meta, bg, 0)
}
