package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

const scrollbarMinPointerTarget = 3

// ScrollbarControl paints a padded vertical or horizontal scrollbar from a ScrollModel.
// This is the single scrollbar paint path for ui collections.
type ScrollbarControl struct {
	Base
	scroll *ScrollModel
	tokens *TokenSet
	// surfaceRole resolves the gutter fill color so the reserved scrollbar
	// column matches the surface it sits on (shell ColorBg, popup
	// ColorPopupBg). NewScrollbarControl defaults it to ColorBg.
	surfaceRole       SemanticColor
	dragging          bool
	dragOffset        int
	direction         Direction
	onScroll          func(oldOffset, newOffset int)
	pointerTargetSize int
}

// NewScrollbarControl creates a scrollbar bound to scroll.
func NewScrollbarControl(id ComponentID, scroll *ScrollModel, tokens *TokenSet) *ScrollbarControl {
	return &ScrollbarControl{
		Base:              NewBase(id),
		scroll:            scroll,
		tokens:            tokens,
		surfaceRole:       ColorBg, // shell default; popups opt into ColorPopupBg
		direction:         Vertical,
		pointerTargetSize: scrollbarMinPointerTarget,
	}
}

// SetTokens updates theme tokens.
func (s *ScrollbarControl) SetTokens(ts *TokenSet) { s.tokens = ts }

// SetSurfaceBackground selects the token role that fills the reserved gutter
// column around the track, mirroring ActionBar. Docked/shell surfaces keep
// the zero-value ColorBg default; popup-hosted surfaces call this with
// ColorPopupBg so the gutter matches the window body fill instead of showing
// as a darker strip.
func (s *ScrollbarControl) SetSurfaceBackground(role SemanticColor) { s.surfaceRole = role }

// SetScroll binds a scroll model.
func (s *ScrollbarControl) SetScroll(m *ScrollModel)         { s.scroll = m }
func (s *ScrollbarControl) SetDirection(direction Direction) { s.direction = direction }

// SetPointerTargetSize caps perpendicular hit expansion for compositions with
// adjacent interactive gutters. Values below the painted width are clamped by
// PointerHitBounds; the default target is three terminal cells.
func (s *ScrollbarControl) SetPointerTargetSize(size int) { s.pointerTargetSize = max(1, size) }

// OnScroll registers a lightweight synchronization hook for consumers that
// mirror ScrollModel.Offset into another authoritative model (file tree,
// terminal scrollback, chat auto-scroll policy). It runs only when the offset
// actually changes.
func (s *ScrollbarControl) OnScroll(fn func(oldOffset, newOffset int)) { s.onScroll = fn }

// PointerHitBounds gives the one-cell stroke a practical target toward its
// content without changing paint or layout geometry. Expansion is left/up
// (toward content); a host that reserves fewer blank columns before the track
// lowers the target with SetPointerTargetSize so a neighbouring control's
// click cannot land on the bar. Remains parent-clipped.
func (s *ScrollbarControl) PointerHitBounds(r Rect) Rect {
	target := max(1, s.pointerTargetSize)
	if s.direction == Horizontal {
		if extra := target - r.H; extra > 0 {
			r.Y -= extra
			r.H += extra
		}
		return r
	}
	if extra := target - r.W; extra > 0 {
		r.X -= extra
		r.W += extra
	}
	return r
}

// Measure: ScrollbarWidth metric columns wide (1 by default), full height.
func (s *ScrollbarControl) Measure(_ LayoutContext, cs Constraints) Size {
	w := 1
	if s.tokens != nil && s.tokens.Metrics().ScrollbarWidth > 0 {
		w = s.tokens.Metrics().ScrollbarWidth
	}
	if s.direction == Horizontal {
		return cs.ClampSize(Size{W: cs.MaxW, H: w})
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
func (s *ScrollbarControl) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := s.Bounds()
	if r.Empty() || s.scroll == nil {
		return
	}
	var bg, trackFG, thumbFG, activeFG, hoverBG cell.Color
	if s.tokens != nil {
		bg = s.tokens.Color(s.surfaceRole)
		trackFG = s.tokens.Color(ColorBorder)
		thumbFG = s.tokens.Color(ColorMuted)
		activeFG = s.tokens.Color(ColorFg)
		hoverBG = s.tokens.Color(ColorHover)
	} else {
		fallback := NewTokenSet(theme.DefaultDark())
		bg = fallback.Color(s.surfaceRole)
		trackFG = fallback.Color(ColorBorder)
		thumbFG = fallback.Color(ColorMuted)
		activeFG = fallback.Color(ColorFg)
		hoverBG = fallback.Color(ColorHover)
	}
	ox, oy := originX+r.X, originY+r.Y
	if !bg.IsDefault() {
		FillRect(buf, 0, 0, Rect{X: ox, Y: oy, W: r.W, H: r.H}, cell.BlankCell(bg))
	}
	if s.scroll.MaxOffset() <= 0 {
		// Fill the reserved gutter so it doesn't show through as a light strip.
		// Settings panels omit this control entirely via Needed().
		return
	}
	trackLen := r.H
	if s.direction == Horizontal {
		trackLen = r.W
	}
	top, th := s.scroll.ThumbBounds(trackLen)
	emphasized := ctx.Hovered || s.dragging
	for pos := 0; pos < trackLen; pos++ {
		if s.direction == Horizontal {
			ch := '▁'
			fg := trackFG
			if pos >= top && pos < top+th {
				ch = '▁'
				fg = thumbFG
				if emphasized {
					ch = '▄'
					fg = activeFG
				}
			}
			buf.Set(ox+pos, oy+r.H/2, cell.NewCell(ch, fg, bg, 0))
			continue
		}
		ch := '│'
		fg := trackFG
		cellBG := bg
		if pos >= top && pos < top+th {
			ch = '┃'
			fg = thumbFG
			if emphasized {
				ch = '█'
				cellBG = hoverBG
			}
		}
		buf.Set(ox+r.W/2, oy+pos, cell.NewCell(ch, fg, cellBG, 0))
	}
}

// Needed reports whether the scrollbar should occupy a column. On-demand
// scrollbars stay hidden when content fits the viewport.
func (s *ScrollbarControl) Needed() bool {
	return s != nil && s.scroll != nil && s.scroll.MaxOffset() > 0
}

// NeededForViewport reports overflow against the viewport being laid out now,
// rather than the ScrollModel's previous-frame viewport. Collection layouts
// use this to avoid fit/overflow flicker and zero-width first-frame bars.
func (s *ScrollbarControl) NeededForViewport(viewport int) bool {
	return s != nil && s.scroll != nil && s.scroll.Content() > max(0, viewport)
}
func (s *ScrollbarControl) IsDragging() bool { return s.dragging }

// CancelPointerCapture ends an in-progress thumb drag without applying scroll.
func (s *ScrollbarControl) CancelPointerCapture() {
	s.dragging = false
	s.dragOffset = 0
}

// Handle maps click/drag on the track to scroll position.
func (s *ScrollbarControl) Handle(ctx EventContext, evt Event) EventResult {
	switch evt.Kind {
	case EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft || s.scroll == nil || s.scroll.MaxOffset() <= 0 {
			return EventResult{}
		}
		coordinate := ctx.LocalY
		if s.direction == Horizontal {
			coordinate = ctx.LocalX
		}
		old := s.scroll.Offset()
		captured := s.beginDrag(coordinate)
		result := EventResult{Handled: true, Invalidate: s.scrollInvalidation(old)}
		if captured {
			result.Action = &Action{ID: "ui.capture"}
		}
		return result
	case EventMouseMotion:
		if !s.dragging || s.scroll == nil {
			return EventResult{}
		}
		coordinate := ctx.LocalY
		if s.direction == Horizontal {
			coordinate = ctx.LocalX
		}
		old := s.scroll.Offset()
		s.dragTo(coordinate)
		return EventResult{Handled: true, Invalidate: s.scrollInvalidation(old)}
	case EventMouseRelease:
		if !s.dragging {
			return EventResult{}
		}
		s.dragging = false
		s.dragOffset = 0
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case EventMouseWheel:
		if s.scroll == nil {
			return EventResult{}
		}
		old := s.scroll.Offset()
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			s.scroll.ScrollBy(-3)
		case tea.MouseWheelDown:
			s.scroll.ScrollBy(3)
		default:
			return EventResult{}
		}
		return EventResult{Handled: true, Invalidate: s.scrollInvalidation(old)}
	}
	return EventResult{}
}

func (s *ScrollbarControl) beginDrag(coordinate int) bool {
	r := s.Bounds()
	trackLen := r.H
	if s.direction == Horizontal {
		trackLen = r.W
	}
	if s.scroll == nil || trackLen <= 0 || s.scroll.MaxOffset() <= 0 {
		return false
	}
	top, th := s.scroll.ThumbBounds(trackLen)
	if coordinate >= top && coordinate < top+th {
		s.dragging = true
		s.dragOffset = coordinate - top
		return true
	}
	page := max(1, s.scroll.Viewport()-1)
	if coordinate < top {
		page = -page
	}
	s.scroll.ScrollBy(page)
	return false
}

func (s *ScrollbarControl) dragTo(coordinate int) {
	if s.scroll == nil {
		return
	}
	r := s.Bounds()
	maxOff := s.scroll.MaxOffset()
	trackLen := r.H
	if s.direction == Horizontal {
		trackLen = r.W
	}
	if maxOff <= 0 || trackLen <= 0 {
		return
	}
	_, th := s.scroll.ThumbBounds(trackLen)
	adjusted := coordinate - s.dragOffset
	if adjusted < 0 {
		adjusted = 0
	}
	available := trackLen - th
	if available <= 0 {
		s.scroll.ScrollTo(0)
		return
	}
	if adjusted > available {
		adjusted = available
	}
	off := adjusted * maxOff / available
	s.scroll.ScrollTo(off)
}

// ScrollToLocalY maps a local Y on the track to a content scroll offset.
func (s *ScrollbarControl) ScrollToLocalY(localY int) {
	s.scrollToCoordinate(localY, s.Bounds().H)
}

func (s *ScrollbarControl) ScrollToLocalX(localX int) {
	s.scrollToCoordinate(localX, s.Bounds().W)
}

func (s *ScrollbarControl) scrollToCoordinate(coordinate, trackLen int) {
	if s.scroll == nil {
		return
	}
	if trackLen <= 1 {
		s.scroll.ScrollTo(0)
		return
	}
	maxOff := s.scroll.MaxOffset()
	if maxOff <= 0 {
		return
	}
	if coordinate < 0 {
		coordinate = 0
	}
	if coordinate >= trackLen {
		coordinate = trackLen - 1
	}
	// Linear map
	off := coordinate * maxOff / (trackLen - 1)
	s.scroll.ScrollTo(off)
}

func (s *ScrollbarControl) scrollInvalidation(oldOffset int) Invalidation {
	if s.scroll == nil || s.scroll.Offset() == oldOffset {
		return InvalidatePaint
	}
	if s.onScroll != nil {
		s.onScroll(oldOffset, s.scroll.Offset())
	}
	// Some collections read Offset in Paint, while retained forms apply it to
	// child bounds in Layout. Scrolling must satisfy both contracts.
	return InvalidateLayout | InvalidatePaint
}

// Semantics for the scrollbar.
func (s *ScrollbarControl) Semantics() Semantics {
	return Semantics{Role: RoleScrollBar, Label: "scrollbar"}
}
