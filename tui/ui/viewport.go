package ui

import (
	"crude/tui/surface"
)

// unboundedCells is the main-axis extent used when measuring viewport content
// with no explicit size (effectively unbounded for terminal layouts).
const unboundedCells = 1 << 20

// Viewport is a scrollable window over a larger child. The child is laid out at
// (-scrollX, -scrollY) so only the visible sub-rect is in-bounds. Paint clips
// the child subtree to the viewport bounds by rendering into a temporary buffer
// and blitting the visible region through a surface.ClipStack.
//
// The viewport is a leaf: it lays out and paints its child subtree itself, so
// HitTest returns the viewport for every cell in its visible rect, matching
// the painted region exactly. Phase 5 adds scroll/drag wiring and can introduce
// clip-aware hit routing for children.
type Viewport struct {
	Base
	child    Component
	scrollX  int
	scrollY  int
	contentW int
	contentH int
	// resolvedW/H is the content size resolved during Layout (needed by Paint,
	// which has no LayoutContext for auto measurement).
	resolvedW int
	resolvedH int
}

// NewViewport creates a scrollable viewport over child.
func NewViewport(id ComponentID, child Component) *Viewport {
	return &Viewport{Base: NewBase(id), child: child}
}

// SetScrollOffset sets the child offset in cells.
func (v *Viewport) SetScrollOffset(x, y int) {
	v.scrollX, v.scrollY = x, y
}

// ScrollOffset returns the current scroll offset.
func (v *Viewport) ScrollOffset() (int, int) { return v.scrollX, v.scrollY }

// SetContentSize sets the child content size explicitly. Zero means measure the
// child during Layout.
func (v *Viewport) SetContentSize(w, h int) {
	v.contentW, v.contentH = w, h
}

// Measure fills the available space.
func (v *Viewport) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: cs.MaxH})
}

// Layout lays the child out at the negative scroll offset.
func (v *Viewport) Layout(ctx LayoutContext, r Rect) {
	v.SetBounds(r)
	if v.child == nil {
		return
	}
	cw, ch := v.contentW, v.contentH
	if cw <= 0 || ch <= 0 {
		sz := v.child.Measure(ctx, Loose(unboundedCells, unboundedCells))
		if cw <= 0 {
			cw = sz.W
		}
		if ch <= 0 {
			ch = sz.H
		}
	}
	v.resolvedW, v.resolvedH = cw, ch
	v.child.Layout(ctx, Rect{X: -v.scrollX, Y: -v.scrollY, W: cw, H: ch})
}

// Paint renders the child subtree into a temporary buffer then blits only the
// cells inside the viewport bounds.
func (v *Viewport) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	if v.child == nil {
		return
	}
	r := v.Bounds()
	if r.Empty() {
		return
	}
	cw, ch := v.resolvedW, v.resolvedH
	if cw < 1 || ch < 1 {
		return
	}
	ox, oy := originX+r.X, originY+r.Y
	// The child is laid out at (-scrollX, -scrollY), so its content occupies
	// temp cells [-scrollX, cw-scrollX) x [-scrollY, ch-scrollY). For a negative
	// scroll offset the right/bottom tail would land past a cw x ch buffer and
	// be silently dropped, so the temp buffer must cover the full child extent.
	tempW := cw
	if v.scrollX < 0 {
		tempW = cw - v.scrollX
	}
	tempH := ch
	if v.scrollY < 0 {
		tempH = ch - v.scrollY
	}
	temp := surface.NewCellBuffer(tempW, tempH)
	PaintTree(v.child, temp, 0, 0, ctx)
	clip := surface.NewClipStack()
	clip.Push(surface.Rect{X: ox, Y: oy, W: r.W, H: r.H})
	surface.Blit(buf, ox, oy, temp, clip)
}

// Handle ignores; as a leaf the viewport receives hits for its whole rect.
func (v *Viewport) Handle(_ EventContext, _ Event) EventResult {
	return EventResult{}
}

// Semantics reports a group role.
func (v *Viewport) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: string(v.ID())}
}
