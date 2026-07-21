package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// ListItem is a row in a List.
type ListItem struct {
	ID        string
	Label     string
	Detail    string
	Icon      string
	Disabled  bool // not activatable; skipped by keyboard nav
	Header    bool // section label; not selectable
	Separator bool // divider row; not selectable
	Payload   any  // optional feature data (e.g. menu section/item index)
}

// Selectable reports whether the item can receive cursor/activation.
func (it ListItem) Selectable() bool {
	return !it.Disabled && !it.Header && !it.Separator
}

// List is a uniform-height selectable list with shared Selection + Scroll models.
type List struct {
	Base
	items    []ListItem
	sel      *SelectionModel
	scroll   *ScrollModel
	tokens   *TokenSet
	activate string // action ID on Enter/click
	hoverIdx int
}

// NewList creates a list (clamp navigation, not wrap).
func NewList(id ComponentID, tokens *TokenSet) *List {
	return &List{
		Base:     NewBase(id),
		sel:      NewSelectionModel(false),
		scroll:   NewScrollModel(),
		tokens:   tokens,
		activate: "list.activate",
		hoverIdx: -1,
	}
}

// SetWrap enables wrap-around navigation (menus) vs clamp (lists).
func (l *List) SetWrap(wrap bool) {
	if l.sel != nil {
		l.sel.SetWrap(wrap)
	}
}

// SetTokens updates theme tokens.
func (l *List) SetTokens(ts *TokenSet) { l.tokens = ts }

// SetItems replaces items and selects the first selectable row if needed.
func (l *List) SetItems(items []ListItem) {
	l.items = items
	l.sel.SetCount(len(items))
	l.scroll.SetContent(len(items))
	if n := l.firstSelectable(); n >= 0 {
		l.sel.SetIndex(n)
	} else {
		l.sel.ClearSelection()
	}
	l.scroll.EnsureVisible(l.sel.Index())
}

// firstSelectable returns the first selectable index, or -1.
func (l *List) firstSelectable() int {
	for i, it := range l.items {
		if it.Selectable() {
			return i
		}
	}
	return -1
}

// moveToSelectable steps from idx in dir (+1/-1) to the next selectable row.
func (l *List) moveToSelectable(from, dir int) int {
	if len(l.items) == 0 || dir == 0 {
		return -1
	}
	n := len(l.items)
	wrap := l.sel != nil && l.sel.wrap
	i := from
	for step := 0; step < n; step++ {
		i += dir
		if i < 0 {
			if !wrap {
				// clamp: walk forward from 0
				for j := 0; j < n; j++ {
					if l.items[j].Selectable() {
						return j
					}
				}
				return -1
			}
			i = n - 1
		} else if i >= n {
			if !wrap {
				for j := n - 1; j >= 0; j-- {
					if l.items[j].Selectable() {
						return j
					}
				}
				return -1
			}
			i = 0
		}
		if l.items[i].Selectable() {
			return i
		}
	}
	return -1
}

// SelectIndex selects idx if selectable.
func (l *List) SelectIndex(idx int) bool {
	if idx < 0 || idx >= len(l.items) || !l.items[idx].Selectable() {
		return false
	}
	l.sel.SetIndex(idx)
	l.scroll.EnsureVisible(idx)
	return true
}

// Items returns the items.
func (l *List) Items() []ListItem { return l.items }

// SelectedIndex returns the selection index.
func (l *List) SelectedIndex() int { return l.sel.Index() }

// Selected returns the selected item and ok.
func (l *List) Selected() (ListItem, bool) {
	i := l.sel.Index()
	if i < 0 || i >= len(l.items) {
		return ListItem{}, false
	}
	return l.items[i], true
}

// SetActivateAction sets the action ID for activation.
func (l *List) SetActivateAction(id string) { l.activate = id }

// Selection returns the selection model.
func (l *List) Selection() *SelectionModel { return l.sel }

// Scroll returns the scroll model.
func (l *List) Scroll() *ScrollModel { return l.scroll }

func (l *List) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: cs.MaxH})
}

func (l *List) Layout(_ LayoutContext, r Rect) {
	l.SetBounds(r)
	l.scroll.SetViewport(r.H)
	l.scroll.EnsureVisible(l.sel.Index())
}

func (l *List) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := l.Bounds()
	if r.Empty() {
		return
	}
	off := l.scroll.Offset()
	vh := l.scroll.Viewport()
	for row := 0; row < vh; row++ {
		idx := off + row
		y := originY + r.Y + row
		x := originX + r.X
		if idx >= len(l.items) {
			// clear remaining
			if l.tokens != nil {
				FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(l.tokens.Color(ColorPopupBg)))
			}
			continue
		}
		item := l.items[idx]
		if item.Separator {
			fg, bg := cell.NoColor, cell.NoColor
			if l.tokens != nil {
				fg = l.tokens.Color(ColorPopupDivider)
				bg = l.tokens.Color(ColorPopupBg)
			}
			if !bg.IsDefault() {
				FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(bg))
			}
			line := strings.Repeat("─", max(0, r.W))
			buf.WriteString(x, y, line, fg, bg, 0)
			continue
		}
		if item.Header {
			fg, bg := cell.NoColor, cell.NoColor
			if l.tokens != nil {
				fg = l.tokens.Color(ColorMuted)
				bg = l.tokens.Color(ColorPopupBg)
			}
			if !bg.IsDefault() {
				FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(bg))
			}
			label := item.Label
			runes := []rune(label)
			if len(runes) > r.W {
				runes = runes[:r.W]
			}
			buf.WriteString(x, y, string(runes), fg, bg, cell.AttrBold)
			continue
		}
		st := StateDefault
		if item.Disabled {
			st = StateDisabled
		} else if idx == l.sel.Index() {
			if ctx.Focused {
				st = StateFocused
			} else {
				st = StateSelected
			}
		} else if idx == l.hoverIdx {
			st = StateHovered
		}
		fg, bg := cell.NoColor, cell.NoColor
		if l.tokens != nil {
			fg, bg = l.tokens.ListRowColors(st)
		}
		if !bg.IsDefault() {
			FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(bg))
		}
		// marker
		marker := "  "
		if idx == l.sel.Index() && item.Selectable() {
			marker = "❯ "
		}
		line := marker
		if item.Icon != "" {
			line += item.Icon + " "
		}
		line += item.Label
		if item.Detail != "" {
			// pad and detail
			used := rendering.VisibleWidth(line)
			gap := r.W - used - rendering.VisibleWidth(item.Detail) - 1
			if gap < 1 {
				gap = 1
			}
			// build with spaces
			spaces := make([]byte, gap)
			for i := range spaces {
				spaces[i] = ' '
			}
			line += string(spaces) + item.Detail
		}
		line = rendering.TruncateLine(line, r.W)
		attr := cell.Attrs(0)
		if idx == l.sel.Index() && item.Selectable() {
			attr = cell.AttrBold
		}
		buf.WriteString(x, y, line, fg, bg, attr)
	}
}

func (l *List) Handle(ctx EventContext, msg tea.Msg) EventResult {
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		switch m.Code {
		case tea.KeyUp:
			next := l.moveToSelectable(l.sel.Index(), -1)
			if next >= 0 {
				l.sel.SetIndex(next)
				l.scroll.EnsureVisible(next)
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyDown:
			next := l.moveToSelectable(l.sel.Index(), 1)
			if next >= 0 {
				l.sel.SetIndex(next)
				l.scroll.EnsureVisible(next)
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyPgUp:
			l.sel.PageUp(l.scroll.Viewport())
			// snap to nearest selectable at or above
			if idx := l.sel.Index(); idx >= 0 && idx < len(l.items) && !l.items[idx].Selectable() {
				if n := l.moveToSelectable(idx, -1); n >= 0 {
					l.sel.SetIndex(n)
				}
			}
			l.scroll.EnsureVisible(l.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyPgDown:
			l.sel.PageDown(l.scroll.Viewport())
			if idx := l.sel.Index(); idx >= 0 && idx < len(l.items) && !l.items[idx].Selectable() {
				if n := l.moveToSelectable(idx, 1); n >= 0 {
					l.sel.SetIndex(n)
				}
			}
			l.scroll.EnsureVisible(l.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyHome:
			if n := l.firstSelectable(); n >= 0 {
				l.sel.SetIndex(n)
				l.scroll.EnsureVisible(n)
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyEnd:
			for i := len(l.items) - 1; i >= 0; i-- {
				if l.items[i].Selectable() {
					l.sel.SetIndex(i)
					l.scroll.EnsureVisible(i)
					break
				}
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyEnter:
			item, ok := l.Selected()
			if !ok || !item.Selectable() {
				return EventResult{Handled: true}
			}
			return EventResult{
				Handled:    true,
				Action:     &Action{ID: l.activate, Payload: item},
				Invalidate: InvalidatePaint,
			}
		}
	case tea.MouseMotionMsg:
		idx := l.scroll.Offset() + ctx.LocalY
		if idx >= 0 && idx < len(l.items) {
			if l.hoverIdx != idx {
				l.hoverIdx = idx
				return EventResult{Handled: true, Invalidate: InvalidatePaint}
			}
		}
		return EventResult{Handled: true}
	case tea.MouseClickMsg:
		if m.Button != tea.MouseLeft {
			return EventResult{}
		}
		idx := l.scroll.Offset() + ctx.LocalY
		if idx >= 0 && idx < len(l.items) {
			item := l.items[idx]
			if !item.Selectable() {
				return EventResult{Handled: true}
			}
			l.sel.SetIndex(idx)
			l.scroll.EnsureVisible(idx)
			return EventResult{
				Handled:    true,
				Action:     &Action{ID: l.activate, Payload: item},
				Invalidate: InvalidatePaint | InvalidateFocus,
			}
		}
	case tea.MouseWheelMsg:
		if m.Button == tea.MouseWheelUp {
			l.scroll.ScrollBy(-3)
		} else if m.Button == tea.MouseWheelDown {
			l.scroll.ScrollBy(3)
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	}
	return EventResult{}
}

func (l *List) Semantics() Semantics {
	item, _ := l.Selected()
	return Semantics{
		Role:     RoleList,
		Label:    string(l.ID()),
		Value:    item.Label,
		Selected: l.sel.Index() >= 0,
	}
}
