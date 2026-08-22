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
	Spacer    bool // empty gap row; not selectable
	Command   bool // button-like row: activates on a single click, not a double
	Depth     int  // optional hierarchy depth for feature-specific row painters
	TreeLast  bool // final sibling at Depth (feature-specific tree connector)
	Payload   any  // optional feature data (e.g. menu section/item index)
}

// Selectable reports whether the item can receive cursor/activation.
func (it ListItem) Selectable() bool {
	return !it.Disabled && !it.Header && !it.Separator && !it.Spacer
}

// List is a uniform-height selectable list with shared Selection + Scroll models.
type List struct {
	Base
	items         []ListItem
	sel           *SelectionModel
	scroll        *ScrollModel
	tokens        *TokenSet
	activate      string // action ID on Enter/click
	hoverIdx      int
	defaultBgRole SemanticColor
	defaultBgSet  bool
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

// SetDefaultRowBackground overrides the background role for rows in the
// default/disabled states (and for empty cells, headers, and separators).
// Interactive states (focused/selected/hovered) keep their token colors.
// Used to give an open select dropdown a raised panel look distinct from the
// surrounding form surface.
func (l *List) SetDefaultRowBackground(role SemanticColor) {
	l.defaultBgRole = role
	l.defaultBgSet = true
}

// SetSelectionMarker is retained for callers. Lists never paint a prompt
// glyph; selection is background + bold only. Rows still indent by FormIndent
// so they sit under section titles.
func (l *List) SetSelectionMarker(_ bool) {}

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

// rowBg resolves the background for non-interactive rows, honoring the
// SetDefaultRowBackground override when configured.
func (l *List) rowBg() cell.Color {
	if l.tokens == nil {
		return cell.NoColor
	}
	if l.defaultBgSet {
		return l.tokens.Color(l.defaultBgRole)
	}
	return l.tokens.Color(ColorPopupBg)
}

func (l *List) itemIndent() int {
	if l.tokens == nil {
		return 1
	}
	return max(0, l.tokens.FormIndent())
}

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
				FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(l.rowBg()))
			}
			continue
		}
		item := l.items[idx]
		if item.Spacer {
			if l.tokens != nil {
				FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(l.rowBg()))
			}
			continue
		}
		if item.Separator {
			fg, bg := cell.NoColor, cell.NoColor
			if l.tokens != nil {
				fg = l.tokens.Color(ColorPopupDivider)
				bg = l.rowBg()
			}
			if !bg.IsDefault() {
				FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(bg))
			}
			line := strings.Repeat(string(FirstRune(l.tokens.Glyphs().BorderH, '─')), max(0, r.W))
			buf.WriteString(x, y, line, fg, bg, 0)
			continue
		}
		if item.Header {
			fg, bg := cell.NoColor, cell.NoColor
			if l.tokens != nil {
				fg = l.tokens.Color(ColorMuted)
				bg = l.rowBg()
			}
			if !bg.IsDefault() {
				FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(bg))
			}
			label := item.Label
			runes := []rune(label)
			if len(runes) > r.W {
				runes = runes[:r.W]
			}
			buf.WriteString(x, y, string(runes), fg, bg, 0)
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
		muted := cell.NoColor
		if l.tokens != nil {
			fg, bg = l.tokens.ListRowColors(st)
			if st == StateDefault || st == StateDisabled {
				bg = l.rowBg()
			}
			fg, muted = l.tokens.MenuInk(st, fg)
		}
		if !bg.IsDefault() {
			FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(bg))
		}
		// Subsection rows sit one column in from section titles. Selection
		// is background + bold only — never a prompt glyph or leading marker.
		prefix := strings.Repeat(" ", l.itemIndent())
		if item.Icon != "" {
			prefix += item.Icon + " "
		}
		attr := cell.Attrs(0)
		if idx == l.sel.Index() && item.Selectable() {
			attr = cell.AttrBold
		}
		if prefix != "" {
			buf.WriteString(x, y, prefix, fg, bg, attr)
		}
		paintMenuDetail(buf, x+rendering.VisibleWidth(prefix), y, r.W-rendering.VisibleWidth(prefix), item.Label, item.Detail, fg, muted, bg, attr)
	}
}

func (l *List) Handle(ctx EventContext, evt Event) EventResult {
	switch evt.Kind {
	case EventKey:
		switch evt.Key.Code {
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
	case EventMouseMotion:
		idx := l.scroll.Offset() + ctx.LocalY
		if idx < 0 || idx >= len(l.items) {
			if l.hoverIdx != -1 {
				l.hoverIdx = -1
			}
			return EventResult{Handled: true}
		}
		if l.hoverIdx != idx {
			l.hoverIdx = idx
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
		return EventResult{Handled: true}
	case EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
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
	case EventMouseWheel:
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			l.scroll.ScrollBy(-3)
		case tea.MouseWheelDown:
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
