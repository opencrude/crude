package surface

import (
	"crude/tui/cell"

	"slices"
)

// Surface is a rectangular rendering surface with hierarchy and z-ordering.
//
// A Surface is the fundamental unit of compositing. It owns a CellBuffer
// and can have child surfaces positioned relative to itself.
//
// Hierarchy model (like Notcurses ncplanes):
//   - Each surface has a position (X, Y) relative to its parent
//   - Children are composited on top of their parent in z-order
//   - Moving a parent moves all children with it
//   - A surface clips its children to its own bounds
//
// Z-Order model:
//   - Children with higher ZOrder are rendered on top
//   - Within the same ZOrder, later children are on top (append order)
//   - The compositor flattens the tree into a z-sorted list for compositing
//
// Transparency model:
//   - A cell with Transparent=true does not overwrite the cell below
//   - A surface with Alpha < 1.0 blends its cells over the parent
type Surface struct {
	id       string
	buffer   *CellBuffer
	x        int        // Position relative to parent
	y        int
	zOrder   int        // Z-axis position (higher = on top among siblings)
	parent   *Surface
	children []*Surface // Ordered by z-order
	visible  bool
	alpha    float32    // 0.0 (invisible) to 1.0 (opaque)
	baseCell cell.Cell  // Default fill cell (background color)
	dirty    bool       // Whether this surface needs re-rendering
}

// NewSurface creates a new surface with the given dimensions and base cell.
// The surface is initially visible with alpha=1.0 and z-order=0.
func NewSurface(id string, w, h int, baseCell cell.Cell) *Surface {
	buf := NewCellBuffer(w, h)
	// Fill with base cell (non-transparent background)
	if !baseCell.Transparent {
		buf.Fill(baseCell)
	}
	return &Surface{
		id:       id,
		buffer:   buf,
		visible:  true,
		alpha:    1.0,
		baseCell: baseCell,
	}
}

// ID returns the surface's unique identifier.
func (s *Surface) ID() string {
	return s.id
}

// Buffer returns the surface's cell buffer for direct cell manipulation.
func (s *Surface) Buffer() *CellBuffer {
	return s.buffer
}

// SetPosition sets the surface position relative to its parent and marks dirty.
func (s *Surface) SetPosition(x, y int) {
	if s.x == x && s.y == y {
		return
	}
	s.x = x
	s.y = y
	s.MarkDirty()
}

// Position returns the position relative to the parent.
func (s *Surface) Position() (int, int) {
	return s.x, s.y
}

// AbsolutePosition returns the absolute screen position.
// Walks up the parent chain summing offsets.
func (s *Surface) AbsolutePosition() (int, int) {
	ax, ay := s.x, s.y
	p := s.parent
	for p != nil {
		ax += p.x
		ay += p.y
		p = p.parent
	}
	return ax, ay
}

// LocalToScreen converts local surface coordinates to screen coordinates.
func (s *Surface) LocalToScreen(lx, ly int) (int, int) {
	ax, ay := s.AbsolutePosition()
	return ax + lx, ay + ly
}

// ScreenToLocal converts screen coordinates to local surface coordinates.
func (s *Surface) ScreenToLocal(sx, sy int) (int, int) {
	ax, ay := s.AbsolutePosition()
	return sx - ax, sy - ay
}

// SetZOrder sets the z-axis position among siblings and marks dirty.
func (s *Surface) SetZOrder(z int) {
	if s.zOrder == z {
		return
	}
	s.zOrder = z
	// Re-sort parent's children list
	if s.parent != nil {
		s.parent.sortChildren()
	}
	s.MarkDirty()
}

// sortChildren sorts the children slice by z-order (ascending).
// Children with the same z-order maintain their relative insertion order.
func (s *Surface) sortChildren() {
	slices.SortStableFunc(s.children, func(a, b *Surface) int {
		if a.zOrder < b.zOrder {
			return -1
		}
		if a.zOrder > b.zOrder {
			return 1
		}
		return 0
	})
}

// AddChild adds a child surface. The child's parent is set to this surface.
// Children are kept sorted by z-order. If the surface is already a child,
// this is a no-op to prevent duplicates.
func (s *Surface) AddChild(child *Surface) {
	// Prevent duplicate addition
	for _, existing := range s.children {
		if existing == child {
			return // Already a child
		}
	}
	child.parent = s
	s.children = append(s.children, child)
	s.sortChildren()
	s.MarkDirty()
}

// RemoveAllChildren removes all child surfaces from this surface.
// This is called at the start of each frame to prevent accumulation.
func (s *Surface) RemoveAllChildren() {
	for _, child := range s.children {
		child.parent = nil
	}
	s.children = s.children[:0]
	s.MarkDirty()
}

// RemoveChild removes a child surface.
func (s *Surface) RemoveChild(child *Surface) {
	for i, c := range s.children {
		if c == child {
			s.children = slices.Delete(s.children, i, i+1)
			child.parent = nil
			s.MarkDirty()
			return
		}
	}
}

// Children returns the list of child surfaces (z-sorted).
func (s *Surface) Children() []*Surface {
	return s.children
}

// SetVisible controls whether this surface is rendered and marks dirty.
func (s *Surface) SetVisible(v bool) {
	if s.visible == v {
		return
	}
	s.visible = v
	s.MarkDirty()
}

// Visible reports whether this surface and all its ancestors are visible.
// A surface is only visible if it and every surface in its parent chain
// are all visible.
func (s *Surface) Visible() bool {
	if !s.visible {
		return false
	}
	if s.parent != nil {
		return s.parent.Visible()
	}
	return true
}

// SetAlpha sets the opacity for compositing. Clamped to [0.0, 1.0].
func (s *Surface) SetAlpha(a float32) {
	if a < 0.0 {
		a = 0.0
	}
	if a > 1.0 {
		a = 1.0
	}
	s.alpha = a
}

// Alpha returns the current opacity.
func (s *Surface) Alpha() float32 {
	return s.alpha
}

// Resize resizes the surface's cell buffer and marks dirty.
func (s *Surface) Resize(w, h int) {
	s.buffer.Resize(w, h)
	s.MarkDirty()
}

// Width returns the buffer width.
func (s *Surface) Width() int {
	return s.buffer.Width()
}

// Height returns the buffer height.
func (s *Surface) Height() int {
	return s.buffer.Height()
}

// MarkDirty marks this surface and walks up all ancestors as needing re-render.
func (s *Surface) MarkDirty() {
	s.dirty = true
	if s.parent != nil {
		s.parent.MarkDirty()
	}
}

// IsDirty reports whether this surface needs re-rendering.
func (s *Surface) IsDirty() bool {
	return s.dirty
}

// ClearDirty clears the dirty flag on this surface.
// It does NOT clear ancestors' dirty flags.
func (s *Surface) ClearDirty() {
	s.dirty = false
}

// ClearDirtyTree clears the dirty flag on this surface and all descendants.
// Called at the end of each frame's Composite() to reset dirty state.
func (s *Surface) ClearDirtyTree() {
	s.dirty = false
	for _, child := range s.children {
		child.ClearDirtyTree()
	}
}

// FlattenZOrder returns all surfaces in the tree in compositing order
// (bottom to top). This is a pre-order traversal where children are
// sorted by z-order (ascending):
//
//	[root, child_z0, child_z0_child0, child_z0_child1, child_z1, child_z1_child0, ...]
//
// The compositor iterates this list front-to-back, compositing each
// surface onto the back buffer.
func (s *Surface) FlattenZOrder() []*Surface {
	var result []*Surface
	s.flattenInto(&result)
	return result
}

// flattenInto appends this surface and its descendants (pre-order, z-sorted)
// into the result slice.
func (s *Surface) flattenInto(result *[]*Surface) {
	*result = append(*result, s)
	for _, child := range s.children {
		child.flattenInto(result)
	}
}

// HitTest returns the topmost surface at the given screen coordinates.
// Traverses children in reverse z-order (highest z first) to find the
// frontmost surface that contains the point.
func (s *Surface) HitTest(screenX, screenY int) *Surface {
	// Convert screen coords to local
	lx, ly := s.ScreenToLocal(screenX, screenY)

	// Check if point is within this surface's bounds
	if lx < 0 || ly < 0 || lx >= s.Width() || ly >= s.Height() {
		return nil
	}

	// Check children in reverse z-order (topmost first)
	for i := len(s.children) - 1; i >= 0; i-- {
		child := s.children[i]
		if !child.visible {
			continue
		}
		if hit := child.HitTest(screenX, screenY); hit != nil {
			return hit
		}
	}

	// No child hit; this surface is the topmost at this point
	return s
}

// Bounds returns the surface's local bounds as a Rect.
func (s *Surface) Bounds() Rect {
	return Rect{X: 0, Y: 0, W: s.Width(), H: s.Height()}
}

// ScreenBounds returns the surface's bounds in screen coordinates.
func (s *Surface) ScreenBounds() Rect {
	ax, ay := s.AbsolutePosition()
	return Rect{X: ax, Y: ay, W: s.Width(), H: s.Height()}
}
