package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

// ScrollbarControl paints a 1-column vertical scrollbar from a ScrollModel.
// This is the single scrollbar paint path for ui collections.
type ScrollbarControl struct {
	Base
	scroll      *ScrollModel
	tokens      *TokenSet
	dragging    bool
	dragOffsetY int
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

// Measure: ScrollbarWidth metric columns wide (1 by default), full height.
func (s *ScrollbarControl) Measure(_ LayoutContext, cs Constraints) Size {
	w := 1
	if s.tokens != nil && s.tokens.Metrics().ScrollbarWidth > 0 {
		w = s.tokens.Metrics().ScrollbarWidth
	}
	return cs.ClampSize(Size{W: w, H: cs.MaxH})
}

// Layout stores bounds and syncs viewport height to track height.
func (s *ScrollbarControl) Layout(_ LayoutContext, r Rect) {
	s.SetBounds(r)
	_ = s.scroll
	_ = r.H // viewport height is owned by the list; track uses Layout bounds
}

// Paint draws track and thumb.
func (s *ScrollbarControl) Paint(buf *surface.CellBuffer, originX, originY int, _ PaintContext) {
	r := s.Bounds()
	if r.Empty() || s.scroll == nil {
		return
	}
	var bg, trackFG, thumbFG cell.Color
	if s.tokens != nil {
		bg = s.tokens.Color(ColorBg)
		trackFG = s.tokens.Color(ColorBorder)
		thumbFG = s.tokens.Color(ColorMuted)
	} else {
		fallback := NewTokenSet(theme.DefaultDark())
		bg = fallback.Color(ColorBg)
		trackFG = fallback.Color(ColorBorder)
		thumbFG = fallback.Color(ColorMuted)
	}
	ox, oy := originX+r.X, originY+r.Y
	if s.scroll.MaxOffset() <= 0 {
		// Still fill the gutter so it doesn't show through as a light strip.
		if !bg.IsDefault() {
			FillRect(buf, 0, 0, Rect{X: ox, Y: oy, W: r.W, H: r.H}, cell.BlankCell(bg))
		}
		return
	}
	top, th := s.scroll.ThumbBounds(r.H)
	for y := 0; y < r.H; y++ {
		ch := '░'
		fg := trackFG
		if y >= top && y < top+th {
			ch = '█'
			fg = thumbFG
		}
		buf.Set(ox, oy+y, cell.NewCell(ch, fg, bg, 0))
	}
}

// IsDragging reports whether the thumb is being dragged.
func (s *ScrollbarControl) IsDragging() bool { return s.dragging }

// CancelPointerCapture ends an in-progress thumb drag without applying scroll.
func (s *ScrollbarControl) CancelPointerCapture() {
	s.dragging = false
	s.dragOffsetY = 0
}

// Handle maps click/drag on the track to scroll position.
func (s *ScrollbarControl) Handle(ctx EventContext, evt Event) EventResult {
	switch evt.Kind {
	case EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft || s.scroll == nil || s.scroll.MaxOffset() <= 0 {
			return EventResult{}
		}
		s.beginDrag(ctx.LocalY)
		return EventResult{Handled: true, Action: &Action{ID: "ui.capture"}, Invalidate: InvalidatePaint}
	case EventMouseMotion:
		if !s.dragging || s.scroll == nil {
			return EventResult{}
		}
		s.dragToLocalY(ctx.LocalY)
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case EventMouseRelease:
		if !s.dragging {
			return EventResult{}
		}
		s.dragging = false
		s.dragOffsetY = 0
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case EventMouseWheel:
		if s.scroll == nil {
			return EventResult{}
		}
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			s.scroll.ScrollBy(-3)
		case tea.MouseWheelDown:
			s.scroll.ScrollBy(3)
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	}
	return EventResult{}
}

func (s *ScrollbarControl) beginDrag(localY int) {
	r := s.Bounds()
	if s.scroll == nil || r.H <= 0 || s.scroll.MaxOffset() <= 0 {
		return
	}
	top, th := s.scroll.ThumbBounds(r.H)
	if localY >= top && localY < top+th {
		s.dragging = true
		s.dragOffsetY = localY - top
		return
	}
	s.ScrollToLocalY(localY)
	top, _ = s.scroll.ThumbBounds(r.H)
	s.dragging = true
	s.dragOffsetY = localY - top
	if s.dragOffsetY < 0 {
		s.dragOffsetY = 0
	}
}

func (s *ScrollbarControl) dragToLocalY(localY int) {
	if s.scroll == nil {
		return
	}
	r := s.Bounds()
	maxOff := s.scroll.MaxOffset()
	if maxOff <= 0 || r.H <= 0 {
		return
	}
	_, th := s.scroll.ThumbBounds(r.H)
	adjustedY := localY - s.dragOffsetY
	if adjustedY < 0 {
		adjustedY = 0
	}
	available := r.H - th
	if available <= 0 {
		s.scroll.ScrollTo(0)
		return
	}
	if adjustedY > available {
		adjustedY = available
	}
	off := adjustedY * maxOff / available
	s.scroll.ScrollTo(off)
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
