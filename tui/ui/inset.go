package ui

import (
	"crude/tui/surface"
	"crude/tui/theme"
)

// Insets is the four-sided padding type. It lives in theme (Spacing.Frame is
// a theme.Insets) and is aliased here so component code keeps the ui.Insets
// spelling.
type Insets = theme.Insets

// InsetsFromSides builds insets from explicit sides.
func InsetsFromSides(t, r, b, l int) Insets {
	return Insets{Top: t, Right: r, Bottom: b, Left: l}
}

// InsetsAll builds uniform insets on all four sides.
func InsetsAll(v int) Insets {
	return Insets{Top: v, Right: v, Bottom: v, Left: v}
}

// Inset pads a single child by a fixed cell margin. The child is laid out at a
// parent-local rect offset by the insets and shrunk by the inset extents.
type Inset struct {
	Base
	insets Insets
	child  Component
}

// NewInset creates an inset container around child.
func NewInset(id ComponentID, insets Insets, child Component) *Inset {
	return &Inset{Base: NewBase(id), insets: insets, child: child}
}

// Children implements Parent.
func (i *Inset) Children() []Component {
	if i.child == nil {
		return nil
	}
	return []Component{i.child}
}

// Measure measures the child with the inset extents removed and adds them back.
func (i *Inset) Measure(ctx LayoutContext, cs Constraints) Size {
	inner := Constraints{
		MinW: max(0, cs.MinW-i.insets.Left-i.insets.Right),
		MaxW: max(0, cs.MaxW-i.insets.Left-i.insets.Right),
		MinH: max(0, cs.MinH-i.insets.Top-i.insets.Bottom),
		MaxH: max(0, cs.MaxH-i.insets.Top-i.insets.Bottom),
	}
	sz := Size{}
	if i.child != nil {
		sz = i.child.Measure(ctx, inner)
	}
	return cs.ClampSize(Size{
		W: sz.W + i.insets.Left + i.insets.Right,
		H: sz.H + i.insets.Top + i.insets.Bottom,
	})
}

// Layout places the child at the inset offset.
func (i *Inset) Layout(ctx LayoutContext, r Rect) {
	i.SetBounds(r)
	if i.child == nil {
		return
	}
	i.child.Layout(ctx, Rect{
		X: i.insets.Left,
		Y: i.insets.Top,
		W: max(0, r.W-i.insets.Left-i.insets.Right),
		H: max(0, r.H-i.insets.Top-i.insets.Bottom),
	})
}

// Paint is a no-op; the child paints itself.
func (i *Inset) Paint(_ *surface.CellBuffer, _, _ int, _ PaintContext) {}

// Handle ignores; Host hit-testing routes to the child.
func (i *Inset) Handle(_ EventContext, _ Event) EventResult {
	return EventResult{}
}

// Semantics reports a group role.
func (i *Inset) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: string(i.ID())}
}
