package ui

import (
	"crude/tui/surface"
)

// Direction for stack layout.
type Direction uint8

const (
	Vertical Direction = iota
	Horizontal
)

// Stack is a simple flex-like container: children laid out in a row or column.
type Stack struct {
	Base
	dir      Direction
	gap      int
	children []Component
	// expandLast stretches the last child to fill remaining main-axis space.
	// Default off: buttons must size to their label, not the dialog width.
	expandLast bool
}

// NewStack creates a stack container.
func NewStack(id ComponentID, dir Direction, gap int, children ...Component) *Stack {
	return &Stack{
		Base:     NewBase(id),
		dir:      dir,
		gap:      gap,
		children: children,
	}
}

// SetExpandLast enables stretching the last child to fill remaining
// main-axis space (opt-in; default off so control rows stay label-sized).
func (s *Stack) SetExpandLast(v bool) { s.expandLast = v }

// Children implements Parent.
func (s *Stack) Children() []Component { return s.children }

// Add appends a child.
func (s *Stack) Add(c Component) {
	s.children = append(s.children, c)
}

// Measure sums children along main axis.
func (s *Stack) Measure(ctx LayoutContext, cs Constraints) Size {
	if len(s.children) == 0 {
		return cs.ClampSize(Size{})
	}
	if s.dir == Horizontal {
		totalW, maxH := 0, 0
		for i, ch := range s.children {
			if i > 0 {
				totalW += s.gap
			}
			// Give each child loose height, remaining width unbounded-ish
			childCS := Constraints{MinW: 0, MaxW: cs.MaxW, MinH: 0, MaxH: cs.MaxH}
			sz := ch.Measure(ctx, childCS)
			totalW += sz.W
			if sz.H > maxH {
				maxH = sz.H
			}
		}
		return cs.ClampSize(Size{W: totalW, H: maxH})
	}
	// Vertical
	totalH, maxW := 0, 0
	for i, ch := range s.children {
		if i > 0 {
			totalH += s.gap
		}
		childCS := Constraints{MinW: 0, MaxW: cs.MaxW, MinH: 0, MaxH: cs.MaxH}
		sz := ch.Measure(ctx, childCS)
		totalH += sz.H
		if sz.W > maxW {
			maxW = sz.W
		}
	}
	return cs.ClampSize(Size{W: maxW, H: totalH})
}

// Layout assigns child bounds.
func (s *Stack) Layout(ctx LayoutContext, r Rect) {
	s.SetBounds(r)
	if s.dir == Horizontal {
		x := 0
		for i, ch := range s.children {
			if i > 0 {
				x += s.gap
			}
			remain := r.W - x
			if remain < 0 {
				remain = 0
			}
			sz := ch.Measure(ctx, Constraints{MinW: 0, MaxW: remain, MinH: 0, MaxH: r.H})
			if s.expandLast && i == len(s.children)-1 && remain > sz.W {
				sz.W = remain // opt-in: last child takes remaining
			}
			ch.Layout(ctx, Rect{X: x, Y: 0, W: sz.W, H: r.H})
			x += sz.W
		}
		return
	}
	y := 0
	for i, ch := range s.children {
		if i > 0 {
			y += s.gap
		}
		remain := r.H - y
		if remain < 0 {
			remain = 0
		}
		sz := ch.Measure(ctx, Constraints{MinW: 0, MaxW: r.W, MinH: 0, MaxH: remain})
		if s.expandLast && i == len(s.children)-1 && remain > sz.H {
			sz.H = remain
		}
		ch.Layout(ctx, Rect{X: 0, Y: y, W: r.W, H: sz.H})
		y += sz.H
	}
}

// Paint is a no-op for the stack itself. Children are painted by Host
// (paintNode) or by PaintTree when used as WindowFrame content.
func (s *Stack) Paint(_ *surface.CellBuffer, _, _ int, _ PaintContext) {}

// Handle routes to children is done by Host hit-testing; stack itself ignores.
func (s *Stack) Handle(_ EventContext, _ Event) EventResult {
	return EventResult{}
}

func (s *Stack) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: string(s.ID())}
}
