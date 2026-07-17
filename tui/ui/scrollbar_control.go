package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
)

// ScrollbarControl paints a 1-column vertical scrollbar from a ScrollModel.
// This is the single scrollbar paint path for ui collections.
type ScrollbarControl struct {
	Base
	scroll *ScrollModel
	tokens *TokenSet
}

// NewScrollbarControl creates a scrollbar bound to scroll.
func NewScrollbarControl(id ComponentID, scroll *ScrollModel, tokens *TokenSet) *ScrollbarControl {
	return &ScrollbarControl{
		Base:   NewBase(id),
		scroll: scroll,
		tokens: tokens,
	}
}

// SetTokens updates theme tokens.
func (s *ScrollbarControl) SetTokens(ts *TokenSet) { s.tokens = ts }

// SetScroll binds a scroll model.
func (s *ScrollbarControl) SetScroll(m *ScrollModel) { s.scroll = m }

// Measure: 1 column wide, full height.
func (s *ScrollbarControl) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: 1, H: cs.MaxH})
}

// Layout stores bounds and syncs viewport height to track height.
func (s *ScrollbarControl) Layout(_ LayoutContext, r Rect) {
	s.SetBounds(r)
	if s.scroll != nil && r.H > 0 {
		// Viewport for content is managed by the list; track height is r.H.
	}
}

// Paint draws track and thumb.
func (s *ScrollbarControl) Paint(buf *surface.CellBuffer, originX, originY int, _ PaintContext) {
	r := s.Bounds()
	if r.Empty() || s.scroll == nil {
		return
	}
	trackFG := cell.NewTrueColor(48, 48, 48)
	thumbFG := cell.NewTrueColor(118, 118, 118)
	if s.tokens != nil {
		trackFG = s.tokens.Color(ColorBorder)
		thumbFG = s.tokens.Color(ColorMuted)
	}
	ox, oy := originX+r.X, originY+r.Y
	top, th := s.scroll.ThumbBounds(r.H)
	for y := 0; y < r.H; y++ {
		ch := '░'
		fg := trackFG
		if y >= top && y < top+th {
			ch = '█'
			fg = thumbFG
		}
		buf.Set(ox, oy+y, cell.NewCell(ch, fg, cell.NoColor, 0))
	}
}

// Handle maps click Y on the track to scroll position.
func (s *ScrollbarControl) Handle(ctx EventContext, msg tea.Msg) EventResult {
	switch m := msg.(type) {
	case tea.MouseClickMsg:
		if m.Button != tea.MouseLeft {
			return EventResult{}
		}
		s.ScrollToLocalY(ctx.LocalY)
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case tea.MouseWheelMsg:
		if s.scroll == nil {
			return EventResult{}
		}
		if m.Button == tea.MouseWheelUp {
			s.scroll.ScrollBy(-3)
		} else if m.Button == tea.MouseWheelDown {
			s.scroll.ScrollBy(3)
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	}
	return EventResult{}
}

// ScrollToLocalY maps a local Y on the track to a content scroll offset.
func (s *ScrollbarControl) ScrollToLocalY(localY int) {
	if s.scroll == nil {
		return
	}
	r := s.Bounds()
	if r.H <= 1 {
		s.scroll.ScrollTo(0)
		return
	}
	maxOff := s.scroll.MaxOffset()
	if maxOff <= 0 {
		return
	}
	if localY < 0 {
		localY = 0
	}
	if localY >= r.H {
		localY = r.H - 1
	}
	// Linear map
	off := localY * maxOff / (r.H - 1)
	s.scroll.ScrollTo(off)
}

// Semantics for the scrollbar.
func (s *ScrollbarControl) Semantics() Semantics {
	return Semantics{Role: RoleScrollBar, Label: "scrollbar"}
}
