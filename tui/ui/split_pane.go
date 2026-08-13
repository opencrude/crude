package ui

import (
	"crude/tui/surface"
)

// SplitPane lays two children along one axis separated by a draggable
// SplitDivider. The divider position is a fraction (0..1) or an absolute
// cell count; pointer capture on the divider mutates the bound SplitModel.
type SplitPane struct {
	Base
	model   *SplitModel
	divider *SplitDivider
	first   Component
	second  Component
}

// NewSplitPane creates a split with a 0.5 fraction and 1-cell divider.
func NewSplitPane(id ComponentID, dir Direction, first, second Component) *SplitPane {
	model := NewSplitModel(dir)
	d := NewSplitDivider(NewID(id, "divider"), model)
	return &SplitPane{
		Base:    NewBase(id),
		model:   model,
		divider: d,
		first:   first,
		second:  second,
	}
}

// Model returns the shared split state.
func (s *SplitPane) Model() *SplitModel { return s.model }

// Divider returns the hit-tested resize handle.
func (s *SplitPane) Divider() *SplitDivider { return s.divider }

// Children implements Parent: first pane, divider, second pane.
func (s *SplitPane) Children() []Component {
	out := make([]Component, 0, 3)
	if s.first != nil {
		out = append(out, s.first)
	}
	if s.divider != nil {
		out = append(out, s.divider)
	}
	if s.second != nil {
		out = append(out, s.second)
	}
	return out
}

// SetFraction sets the divider as a fraction of the main axis (clamped to 0..1)
// and clears any absolute position.
func (s *SplitPane) SetFraction(f float64) {
	s.model.SetFraction(f)
}

// SetDividerPosition sets the divider at an absolute main-axis cell position.
func (s *SplitPane) SetDividerPosition(p int) {
	s.model.SetFirstSize(p)
}

// SetDividerSize sets the divider width/height in cells.
func (s *SplitPane) SetDividerSize(d int) {
	s.model.SetDividerSize(d)
}

// SetMinSizes sets the minimum first and second pane sizes.
func (s *SplitPane) SetMinSizes(first, second int) {
	s.model.SetMinSizes(first, second)
}

// SetTokens forwards theme tokens to the divider.
func (s *SplitPane) SetTokens(ts *TokenSet) {
	if s.divider != nil {
		s.divider.SetTokens(ts)
	}
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
	div := s.model.DividerSize()
	if s.model.Dir() == Horizontal {
		return cs.ClampSize(Size{
			W: firstSz.W + div + secondSz.W,
			H: max(firstSz.H, secondSz.H),
		})
	}
	return cs.ClampSize(Size{
		W: max(firstSz.W, secondSz.W),
		H: firstSz.H + div + secondSz.H,
	})
}

// Layout assigns parent-local rects to both panes and the divider.
func (s *SplitPane) Layout(ctx LayoutContext, r Rect) {
	s.SetBounds(r)
	mainTotal, crossTotal := r.W, r.H
	if s.model.Dir() == Vertical {
		mainTotal, crossTotal = r.H, r.W
	}
	firstMain, divider, secondMain := s.model.Resolve(mainTotal)
	if s.divider != nil {
		s.divider.SetCurrentFirst(firstMain)
	}
	if s.model.Dir() == Horizontal {
		if s.first != nil {
			s.first.Layout(ctx, Rect{X: 0, Y: 0, W: firstMain, H: crossTotal})
		}
		if s.divider != nil {
			s.divider.Layout(ctx, Rect{X: firstMain, Y: 0, W: divider, H: crossTotal})
		}
		if s.second != nil {
			s.second.Layout(ctx, Rect{X: firstMain + divider, Y: 0, W: secondMain, H: crossTotal})
		}
		return
	}
	if s.first != nil {
		s.first.Layout(ctx, Rect{X: 0, Y: 0, W: crossTotal, H: firstMain})
	}
	if s.divider != nil {
		s.divider.Layout(ctx, Rect{X: 0, Y: firstMain, W: crossTotal, H: divider})
	}
	if s.second != nil {
		s.second.Layout(ctx, Rect{X: 0, Y: firstMain + divider, W: crossTotal, H: secondMain})
	}
}

// Paint is a no-op; panes and the divider are painted by Host.paintNode.
func (s *SplitPane) Paint(_ *surface.CellBuffer, _, _ int, _ PaintContext) {}

// Handle ignores; Host hit-testing routes to the panes or the divider.
func (s *SplitPane) Handle(_ EventContext, _ Event) EventResult {
	return EventResult{}
}

// Semantics reports a group role.
func (s *SplitPane) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: string(s.ID())}
}

var _ Component = (*SplitPane)(nil)
var _ Parent = (*SplitPane)(nil)
