package activitylist

import (
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID    ui.ComponentID = "activitylist"
	ListID      ui.ComponentID = "activitylist/list"
	ScrollbarID ui.ComponentID = "activitylist/scrollbar"
	DividerID   ui.ComponentID = "activitylist/divider"
	TitleID     ui.ComponentID = "activitylist/title"

	ActionActivate = "sidebar.activate"
)

// Screen is the sidebar body for Agents, Terminals, and Git.
type Screen struct {
	ui.Base
	th        *theme.Theme
	tokens    *ui.TokenSet
	title     *ui.Label
	list      *ui.List
	scrollbar *ui.ScrollbarControl
	split     *ui.SplitModel
	divider   *ui.SplitDivider
	heading   string
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{
		Base:    ui.NewBase(ScreenID),
		th:      th,
		tokens:  tokens,
		heading: "Files",
	}
	s.title = ui.NewLabel(TitleID, s.heading, th)
	s.title.SetColorRole(ui.ColorMuted)
	s.list = ui.NewList(ListID, tokens)
	s.list.SetActivateAction(ActionActivate)
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, s.list.Scroll(), tokens)
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.split.SetMinSizes(12, 0)
	s.divider = ui.NewSplitDivider(DividerID, s.split)
	s.divider.SetTokens(tokens)
	return s
}

func (s *Screen) SetHeading(h string) {
	s.heading = h
	if s.title != nil {
		s.title.SetText(h)
	}
}

func (s *Screen) SetItems(items []ui.ListItem) {
	if s.list != nil {
		s.list.SetItems(items)
	}
}

func (s *Screen) SetSplitMaxFirst(n int) {
	if s.split != nil {
		s.split.SetMaxFirst(n)
	}
}

func (s *Screen) SplitFirst() int {
	if s.split == nil {
		return 0
	}
	return s.split.FirstSize()
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.list != nil {
		s.list.SetTokens(tokens)
	}
	if s.scrollbar != nil {
		s.scrollbar.SetTokens(tokens)
	}
	if s.divider != nil {
		s.divider.SetTokens(tokens)
	}
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
	if s.title != nil {
		s.title.SetTokens(ui.NewTokenSet(th))
	}
}

func (s *Screen) Children() []ui.Component {
	out := make([]ui.Component, 0, 4)
	if s.title != nil {
		out = append(out, s.title)
	}
	if s.list != nil {
		out = append(out, s.list)
	}
	if s.scrollbar != nil {
		out = append(out, s.scrollbar)
	}
	if s.divider != nil {
		out = append(out, s.divider)
	}
	return out
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	contentW := r.W
	if r.W > 1 {
		contentW = r.W - 1
	}
	if s.title != nil {
		s.title.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: contentW, H: 1})
	}
	listY, listH := 1, r.H-1
	if listH < 1 {
		listH = 1
		listY = 0
	}
	if s.list != nil {
		s.list.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: listY, W: max(1, contentW-1), H: listH})
	}
	if s.scrollbar != nil && contentW >= 1 {
		s.scrollbar.Layout(ui.LayoutContext{}, ui.Rect{X: contentW - 1, Y: listY, W: 1, H: listH})
	}
	if s.divider != nil && r.W > 0 {
		if s.split != nil && !s.split.Dragging() {
			s.split.SetFirstSize(r.W - 1)
		}
		s.divider.SetCurrentFirst(r.W - 1)
		s.divider.Layout(ui.LayoutContext{}, ui.Rect{X: r.W - 1, Y: 0, W: 1, H: r.H})
	}
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	bg := cell.NoColor
	if s.tokens != nil {
		bg = s.tokens.Color(ui.ColorBg)
	}
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
}

func (s *Screen) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult {
	return ui.EventResult{}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: s.heading}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
