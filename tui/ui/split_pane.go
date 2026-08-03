package ui

import (
	"crude/tui/surface"
)

// SplitPane lays two children along one axis separated by a divider. The
// divider position is controlled by a fraction (0..1) or an absolute cell
// position. Drag wiring is a Phase 5 concern; the position setters exist so
// drag handling can mutate them.
type SplitPane struct {
	Base
	dir         Direction // Horizontal = side-by-side, Vertical = stacked
	fraction    float64
	dividerSize int
	dividerPos  int // absolute main-axis position; -1 = derive from fraction
	first       Component
	second      Component
}

// NewSplitPane creates a split with a 0.5 fraction and 1-cell divider.
func NewSplitPane(id ComponentID, dir Direction, first, second Component) *SplitPane {
	return &SplitPane{
		Base:        NewBase(id),
		dir:         dir,
		fraction:    0.5,
		dividerSize: 1,
		dividerPos:  -1,
		first:       first,
		second:      second,
	}
}

// Children implements Parent.
func (s *SplitPane) Children() []Component {
	out := make([]Component, 0, 2)
	if s.first != nil {
		out = append(out, s.first)
	}
	if s.second != nil {
		out = append(out, s.second)
	}
	return out
}

// SetFraction sets the divider as a fraction of the main axis (clamped to 0..1)
// and clears any absolute position.
func (s *SplitPane) SetFraction(f float64) {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	s.fraction = f
	s.dividerPos = -1
}

// SetDividerPosition sets the divider at an absolute main-axis cell position.
func (s *SplitPane) SetDividerPosition(p int) {
	if p < 0 {
		p = 0
	}
	s.dividerPos = p
}

// SetDividerSize sets the divider width/height in cells.
func (s *SplitPane) SetDividerSize(d int) {
	if d < 0 {
		d = 0
	}
	s.dividerSize = d
}

// Measure sums the two panes and the divider along the main axis.
func (s *SplitPane) Measure(ctx LayoutContext, cs Constraints) Size {
	var firstSz, secondSz Size
	if s.first != nil {
		firstSz = s.first.Measure(ctx, Loose(cs.MaxW, cs.MaxH))
	}
	if s.second != nil {
		secondSz = s.second.Measure(ctx, Loose(cs.MaxW, cs.MaxH))
	}
	if s.dir == Horizontal {
		return cs.ClampSize(Size{
			W: firstSz.W + s.dividerSize + secondSz.W,
			H: max(firstSz.H, secondSz.H),
		})
	}
	return cs.ClampSize(Size{
		W: max(firstSz.W, secondSz.W),
		H: firstSz.H + s.dividerSize + secondSz.H,
	})
}

// Layout assigns parent-local rects to both panes.
func (s *SplitPane) Layout(ctx LayoutContext, r Rect) {
	s.SetBounds(r)
	if s.first == nil && s.second == nil {
		return
	}
	mainTotal, crossTotal := r.W, r.H
	if s.dir == Vertical {
		mainTotal, crossTotal = r.H, r.W
	}
	divider := s.dividerSize
	if divider > mainTotal {
		divider = mainTotal
	}
	firstMain := int(float64(mainTotal) * s.fraction)
	if s.dividerPos >= 0 {
		firstMain = s.dividerPos
	}
	if firstMain < 0 {
		firstMain = 0
	}
	if remain := mainTotal - divider; firstMain > remain {
		firstMain = remain
	}
	secondMain := mainTotal - divider - firstMain
	if secondMain < 0 {
		secondMain = 0
	}
	if s.dir == Horizontal {
		if s.first != nil {
			s.first.Layout(ctx, Rect{X: 0, Y: 0, W: firstMain, H: crossTotal})
		}
		if s.second != nil {
			s.second.Layout(ctx, Rect{X: firstMain + divider, Y: 0, W: secondMain, H: crossTotal})
		}
		return
	}
	if s.first != nil {
		s.first.Layout(ctx, Rect{X: 0, Y: 0, W: crossTotal, H: firstMain})
	}
	if s.second != nil {
		s.second.Layout(ctx, Rect{X: 0, Y: firstMain + divider, W: crossTotal, H: secondMain})
	}
}

// Paint is a no-op; panes are painted by Host or PaintTree.
func (s *SplitPane) Paint(_ *surface.CellBuffer, _, _ int, _ PaintContext) {}

// Handle ignores; Host hit-testing routes to the panes.
func (s *SplitPane) Handle(_ EventContext, _ Event) EventResult {
	return EventResult{}
}

// Semantics reports a group role.
func (s *SplitPane) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: string(s.ID())}
}
