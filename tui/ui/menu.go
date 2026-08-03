package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// MenuItem is one entry in a Menu.
type MenuItem struct {
	ID        string
	Label     string
	Accel     string // right-aligned keyboard shortcut hint (e.g. "Ctrl+Shift+F")
	Submenu   bool   // renders a trailing ▸ indicator
	Disabled  bool
	Header    bool // section label; not selectable
	Separator bool // divider row; not selectable
	Payload   any
}

// Selectable reports whether the item can receive cursor/activation.
func (it MenuItem) Selectable() bool {
	return !it.Disabled && !it.Header && !it.Separator
}

// Menu is a vertical action list sharing Selection + Scroll models with List.
// It adds menu semantics over List's row model: wrap navigation by default,
// Esc dismisses, and items may render an accelerator hint and a submenu
// indicator. Page/Home/End and ensure-visible come from the shared models.
type Menu struct {
	Base
	items    []MenuItem
	sel      *SelectionModel
	scroll   *ScrollModel
	tokens   *TokenSet
	hoverIdx int
	activate string
	dismiss  string
}

// NewMenu creates a menu. Navigation wraps at the edges, matching the
// established context-menu behavior.
func NewMenu(id ComponentID, tokens *TokenSet) *Menu {
	return &Menu{
		Base:     NewBase(id),
		sel:      NewSelectionModel(true),
		scroll:   NewScrollModel(),
		tokens:   tokens,
		hoverIdx: -1,
		activate: "menu.activate",
		dismiss:  "menu.dismiss",
	}
}

// SetTokens updates theme tokens.
func (m *Menu) SetTokens(ts *TokenSet) { m.tokens = ts }

// SetItems replaces items and selects the first selectable row if needed.
func (m *Menu) SetItems(items []MenuItem) {
	m.items = items
	m.sel.SetCount(len(items))
	m.scroll.SetContent(len(items))
	if n := m.firstSelectable(); n >= 0 {
		m.sel.SetIndex(n)
	} else {
		m.sel.ClearSelection()
	}
	m.scroll.EnsureVisible(m.sel.Index())
}

// SetWrap controls wrap-around navigation (on by default for menus).
func (m *Menu) SetWrap(wrap bool) {
	if m.sel != nil {
		m.sel.SetWrap(wrap)
	}
}

// SetActivateAction sets the action ID emitted on item activation.
func (m *Menu) SetActivateAction(id string) { m.activate = id }

// SetDismissAction sets the action ID emitted on Esc.
func (m *Menu) SetDismissAction(id string) { m.dismiss = id }

// Items returns the items.
func (m *Menu) Items() []MenuItem { return m.items }

// SelectIndex selects idx if selectable.
func (m *Menu) SelectIndex(idx int) bool {
	if idx < 0 || idx >= len(m.items) || !m.items[idx].Selectable() {
		return false
	}
	m.sel.SetIndex(idx)
	m.scroll.EnsureVisible(idx)
	return true
}

// SelectedIndex returns the selection index.
func (m *Menu) SelectedIndex() int { return m.sel.Index() }

// Selected returns the selected item and ok.
func (m *Menu) Selected() (MenuItem, bool) {
	i := m.sel.Index()
	if i < 0 || i >= len(m.items) {
		return MenuItem{}, false
	}
	return m.items[i], true
}

// Selection returns the shared selection model.
func (m *Menu) Selection() *SelectionModel { return m.sel }

// Scroll returns the shared scroll model.
func (m *Menu) Scroll() *ScrollModel { return m.scroll }

// firstSelectable returns the first selectable index, or -1.
func (m *Menu) firstSelectable() int {
	for i, it := range m.items {
		if it.Selectable() {
			return i
		}
	}
	return -1
}

// moveToSelectable steps from idx in dir (+1/-1) to the next selectable row.
func (m *Menu) moveToSelectable(from, dir int) int {
	if len(m.items) == 0 || dir == 0 {
		return -1
	}
	n := len(m.items)
	wrap := m.sel != nil && m.sel.wrap
	i := from
	for step := 0; step < n; step++ {
		i += dir
		if i < 0 {
			if !wrap {
				for j := 0; j < n; j++ {
					if m.items[j].Selectable() {
						return j
					}
				}
				return -1
			}
			i = n - 1
		} else if i >= n {
			if !wrap {
				for j := n - 1; j >= 0; j-- {
					if m.items[j].Selectable() {
						return j
					}
				}
				return -1
			}
			i = 0
		}
		if m.items[i].Selectable() {
			return i
		}
	}
	return -1
}

func (m *Menu) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: cs.MaxH})
}

func (m *Menu) Layout(_ LayoutContext, r Rect) {
	m.SetBounds(r)
	m.scroll.SetViewport(r.H)
	m.scroll.EnsureVisible(m.sel.Index())
}

func (m *Menu) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := m.Bounds()
	if r.Empty() {
		return
	}
	off := m.scroll.Offset()
	vh := m.scroll.Viewport()
	for row := 0; row < vh; row++ {
		idx := off + row
		y := originY + r.Y + row
		x := originX + r.X
		if idx >= len(m.items) {
			if m.tokens != nil {
				FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(m.tokens.Color(ColorPopupBg)))
			}
			continue
		}
		item := m.items[idx]
		if item.Separator {
			fg, bg := cell.NoColor, cell.NoColor
			if m.tokens != nil {
				fg = m.tokens.Color(ColorPopupDivider)
				bg = m.tokens.Color(ColorPopupBg)
			}
			if !bg.IsDefault() {
				FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(bg))
			}
			line := strings.Repeat(string(FirstRune(m.tokens.Glyphs().BorderH, '─')), max(0, r.W))
			buf.WriteString(x, y, line, fg, bg, 0)
			continue
		}
		if item.Header {
			fg, bg := cell.NoColor, cell.NoColor
			if m.tokens != nil {
				fg = m.tokens.Color(ColorMuted)
				bg = m.tokens.Color(ColorPopupBg)
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
		} else if idx == m.sel.Index() {
			if ctx.Focused {
				st = StateFocused
			} else {
				st = StateSelected
			}
		} else if idx == m.hoverIdx {
			st = StateHovered
		}
		fg, bg := cell.NoColor, cell.NoColor
		if m.tokens != nil {
			fg, bg = m.tokens.ListRowColors(st)
		}
		if !bg.IsDefault() {
			FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(bg))
		}
		marker := "  "
		if idx == m.sel.Index() && item.Selectable() {
			marker = string(FirstRune(m.tokens.Glyphs().Prompt, '❯')) + " "
		}
		label := marker + item.Label
		attr := cell.Attrs(0)
		if idx == m.sel.Index() && item.Selectable() {
			attr = cell.AttrBold
		}
		trailing := ""
		if item.Submenu {
			trailing = string(FirstRune(m.tokens.Glyphs().TreeCollapsed, '▸'))
		} else if item.Accel != "" {
			trailing = item.Accel
		}
		labelMax := r.W
		if trailing != "" {
			labelMax -= rendering.VisibleWidth(trailing) + 1
		}
		if labelMax < 1 {
			labelMax = 1
		}
		label = rendering.TruncateLine(label, labelMax)
		line := label
		if trailing != "" {
			gap := r.W - rendering.VisibleWidth(label) - rendering.VisibleWidth(trailing)
			if gap < 1 {
				gap = 1
			}
			line += strings.Repeat(" ", gap) + trailing
		}
		line = rendering.TruncateLine(line, r.W)
		subFg := fg
		if item.Submenu && m.tokens != nil {
			subFg = m.tokens.Color(ColorAccent)
		}
		buf.WriteString(x, y, line, fg, bg, attr)
		if item.Submenu && trailing != "" {
			sx := x + r.W - rendering.VisibleWidth(trailing)
			buf.WriteString(sx, y, trailing, subFg, bg, attr)
		}
	}
}

func (m *Menu) Handle(ctx EventContext, evt Event) EventResult {
	switch evt.Kind {
	case EventKey:
		switch evt.Key.Code {
		case tea.KeyEsc:
			return EventResult{Handled: true, Action: &Action{ID: m.dismiss}, Invalidate: InvalidatePaint}
		case tea.KeyUp:
			next := m.moveToSelectable(m.sel.Index(), -1)
			if next >= 0 {
				m.sel.SetIndex(next)
				m.scroll.EnsureVisible(next)
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyDown:
			next := m.moveToSelectable(m.sel.Index(), 1)
			if next >= 0 {
				m.sel.SetIndex(next)
				m.scroll.EnsureVisible(next)
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyPgUp:
			m.sel.PageUp(m.scroll.Viewport())
			if idx := m.sel.Index(); idx >= 0 && idx < len(m.items) && !m.items[idx].Selectable() {
				if n := m.moveToSelectable(idx, -1); n >= 0 {
					m.sel.SetIndex(n)
				}
			}
			m.scroll.EnsureVisible(m.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyPgDown:
			m.sel.PageDown(m.scroll.Viewport())
			if idx := m.sel.Index(); idx >= 0 && idx < len(m.items) && !m.items[idx].Selectable() {
				if n := m.moveToSelectable(idx, 1); n >= 0 {
					m.sel.SetIndex(n)
				}
			}
			m.scroll.EnsureVisible(m.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyHome:
			if n := m.firstSelectable(); n >= 0 {
				m.sel.SetIndex(n)
				m.scroll.EnsureVisible(n)
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyEnd:
			for i := len(m.items) - 1; i >= 0; i-- {
				if m.items[i].Selectable() {
					m.sel.SetIndex(i)
					m.scroll.EnsureVisible(i)
					break
				}
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyEnter:
			item, ok := m.Selected()
			if !ok || !item.Selectable() {
				return EventResult{Handled: true}
			}
			return EventResult{
				Handled:    true,
				Action:     &Action{ID: m.activate, Payload: item},
				Invalidate: InvalidatePaint,
			}
		}
	case EventMouseMotion:
		idx := m.scroll.Offset() + ctx.LocalY
		if idx >= 0 && idx < len(m.items) {
			if m.hoverIdx != idx {
				m.hoverIdx = idx
				return EventResult{Handled: true, Invalidate: InvalidatePaint}
			}
		}
		return EventResult{Handled: true}
	case EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return EventResult{}
		}
		idx := m.scroll.Offset() + ctx.LocalY
		if idx >= 0 && idx < len(m.items) {
			item := m.items[idx]
			if !item.Selectable() {
				return EventResult{Handled: true}
			}
			m.sel.SetIndex(idx)
			m.scroll.EnsureVisible(idx)
			return EventResult{
				Handled:    true,
				Action:     &Action{ID: m.activate, Payload: item},
				Invalidate: InvalidatePaint | InvalidateFocus,
			}
		}
	case EventMouseWheel:
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			m.scroll.ScrollBy(-3)
		case tea.MouseWheelDown:
			m.scroll.ScrollBy(3)
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	}
	return EventResult{}
}

func (m *Menu) Semantics() Semantics {
	item, _ := m.Selected()
	return Semantics{
		Role:     RoleList,
		Label:    string(m.ID()),
		Value:    item.Label,
		Selected: m.sel.Index() >= 0,
	}
}
