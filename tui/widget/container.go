package widget

import (
	"fmt"
	"sort"

	"crude/tui/cell"
	"crude/tui/surface"
)

// Direction controls the axis along which a StackLayout arranges its children.
type Direction int

const (
	// Vertical arranges children top-to-bottom.
	Vertical Direction = iota
	// Horizontal arranges children left-to-right.
	Horizontal
)

// ChildWidget is a widget with position/size within its parent container.
type ChildWidget struct {
	// Widget is the child widget to be rendered.
	Widget Widget

	// X and Y are the position of the child within the parent, in cell coordinates.
	// Set by the Layout during Arrange.
	X, Y int

	// W and H are the allocated size of the child, in cells.
	// Set by the Layout during Arrange.
	W, H int

	// ZOrder controls the stacking order among siblings.
	// Children with higher ZOrder are rendered on top.
	// Within the same ZOrder, later children are on top (append order).
	ZOrder int

	// Visible controls whether this child is rendered.
	// Invisible children still participate in layout (arrange) but
	// are skipped during Render.
	Visible bool
}

// Layout determines how children are positioned within a Container.
type Layout interface {
	// Arrange computes positions (X, Y) and sizes (W, H) for all children
	// given the available space. The method mutates the children slice
	// in place, setting each ChildWidget's X, Y, W, H fields.
	Arrange(children []ChildWidget, availW, availH int)
}

// ---------------------------------------------------------------------------
// StackLayout
// ---------------------------------------------------------------------------

// StackLayout arranges children in a vertical or horizontal stack with
// an optional gap between them.
//
// In Vertical mode, children are laid out top-to-bottom. Each child gets
// the full available width. The height is distributed proportionally: each
// child gets its preferred height (from Measure), and remaining space is
// given to the last child.
//
// In Horizontal mode, children are laid out left-to-right. Each child gets
// the full available height. The width is distributed similarly.
type StackLayout struct {
	// Direction is the stacking axis: Vertical (top-to-bottom) or Horizontal (left-to-right).
	Direction Direction

	// Gap is the number of blank cells between consecutive children.
	Gap int
}

// Arrange positions children along the stack axis.
func (l *StackLayout) Arrange(children []ChildWidget, availW, availH int) {
	if len(children) == 0 {
		return
	}

	totalGap := l.Gap * (len(children) - 1)
	if totalGap < 0 {
		totalGap = 0
	}

	// First pass: measure each child's preferred size.
	prefs := make([][2]int, len(children))
	for i, cw := range children {
		var cwW, cwH int
		if l.Direction == Vertical {
			cwH = max(availH-totalGap, 0)
			cwW = availW
		} else {
			cwW = max(availW-totalGap, 0)
			cwH = availH
		}
		pw, ph := cw.Widget.Measure(cwW, cwH)
		prefs[i] = [2]int{pw, ph}
	}

	// Second pass: assign positions and sizes.
	x, y := 0, 0
	for i := range children {
		if l.Direction == Vertical {
			children[i].X = x
			children[i].Y = y
			children[i].W = availW
			// For the last child, give remaining space; otherwise use preferred height.
			if i == len(children)-1 {
				children[i].H = max(availH-y, 0)
			} else {
				children[i].H = min(prefs[i][1], max(availH-y, 0))
			}
			y += children[i].H + l.Gap
		} else {
			children[i].X = x
			children[i].Y = y
			children[i].H = availH
			// For the last child, give remaining space; otherwise use preferred width.
			if i == len(children)-1 {
				children[i].W = max(availW-x, 0)
			} else {
				children[i].W = min(prefs[i][0], max(availW-x, 0))
			}
			x += children[i].W + l.Gap
		}
	}
}

// ---------------------------------------------------------------------------
// OverlayLayout
// ---------------------------------------------------------------------------

// OverlayLayout stacks all children on top of each other at the same
// position and size, composited in z-order.
//
// This is useful for layered UIs where one widget is drawn over another
// (e.g., a background panel with a foreground overlay). The topmost child
// (highest ZOrder) receives focus.
type OverlayLayout struct{}

// Arrange positions all children at (0,0) with the full available size.
func (OverlayLayout) Arrange(children []ChildWidget, availW, availH int) {
	for i := range children {
		children[i].X = 0
		children[i].Y = 0
		children[i].W = availW
		children[i].H = availH
	}
}

// ---------------------------------------------------------------------------
// FreeLayout
// ---------------------------------------------------------------------------

// FreeLayout is a no-op layout where children are positioned manually.
// The child's X, Y, W, H must be set directly on each ChildWidget before
// Render is called. Arrange does nothing.
//
// This is useful for absolute positioning scenarios such as floating
// elements, drag-and-drop containers, or custom layouts that don't fit
// the stack/overlay models.
type FreeLayout struct{}

// Arrange is a no-op for FreeLayout; children retain their manually set positions.
func (FreeLayout) Arrange(_ []ChildWidget, _, _ int) {}

// ---------------------------------------------------------------------------
// Container
// ---------------------------------------------------------------------------

// Container is a widget that holds and layouts child widgets.
//
// Container is the foundation for nested composition. A Container
// creates a sub-surface for each child and renders children into
// their allocated sub-surfaces. The sub-surfaces are then composited
// onto the container's own surface.
//
// This enables virtually infinite nesting:
//
//	App → Container → FloatingWindow → Container → TabBar + Content
//	  → Container → SplitPane → Container → Widget
type Container struct {
	id       string
	children []ChildWidget
	layout   Layout
}

// NewContainer creates a container with the given id and layout.
func NewContainer(id string, layout Layout) *Container {
	return &Container{
		id:     id,
		layout: layout,
	}
}

// AddChild adds a child widget with the given z-order.
// The child is appended to the end of the children list and is visible by default.
func (c *Container) AddChild(w Widget, zOrder int) {
	c.children = append(c.children, ChildWidget{
		Widget:  w,
		ZOrder:  zOrder,
		Visible: true,
	})
}

// SetLayout changes the container's layout. The new layout takes effect
// on the next Render call.
func (c *Container) SetLayout(l Layout) {
	c.layout = l
}

// Children returns the current list of child widgets.
func (c *Container) Children() []ChildWidget {
	return c.children
}

// ID returns the container's identifier.
func (c *Container) ID() string {
	return c.id
}

// Render implements Widget. It performs the following steps:
//  1. Calls layout.Arrange() to compute child positions/sizes
//  2. Sorts visible children by z-order
//  3. For each visible child, creates a sub-surface on the parent surface
//     at the child's position, sized to the child's W×H
//  4. Calls child.Render(subSurface, childCtx)
//  5. The sub-surfaces are naturally composited because they are children
//     of the container's surface
func (c *Container) Render(surf *surface.Surface, ctx *RenderContext) {
	// Clear previous frame's children to prevent accumulation.
	surf.RemoveAllChildren()

	if c.layout != nil {
		c.layout.Arrange(c.children, surf.Width(), surf.Height())
	}

	// Collect visible children sorted by z-order (bottom to top).
	visible := make([]int, 0, len(c.children))
	for i := range c.children {
		if c.children[i].Visible {
			visible = append(visible, i)
		}
	}
	sort.Slice(visible, func(a, b int) bool {
		return c.children[visible[a]].ZOrder < c.children[visible[b]].ZOrder
	})

	for _, idx := range visible {
		cw := &c.children[idx]
		if cw.W <= 0 || cw.H <= 0 {
			continue
		}

		// Create a child surface positioned at (cw.X, cw.Y) relative to
		// the container, sized to cw.W × cw.H.
		childID := fmt.Sprintf("%s-child-%d", c.id, idx)
		childSurf := surface.NewSurface(childID, cw.W, cw.H, cell.TransparentBlank())
		childSurf.SetPosition(cw.X, cw.Y)
		childSurf.SetZOrder(cw.ZOrder)

		// Build child render context.
		childCtx := ctx
		if ctx.Opacity < 1.0 {
			childCtx = ctx.WithOpacity(1.0) // Children start fresh within their surface
		}

		// Push clip for the child's allocated region.
		clipRect := surface.Rect{X: cw.X, Y: cw.Y, W: cw.W, H: cw.H}
		pop := ctx.PushClip(clipRect)

		// Render the child into the sub-surface.
		cw.Widget.Render(childSurf, childCtx)

		// Attach the child surface to the container's surface for compositing.
		surf.AddChild(childSurf)

		pop()
	}
}

// Measure implements Widget. It returns the size needed by the container's
// children according to the layout strategy:
// - StackLayout: sum of child sizes along the stack axis, max on the other
// - OverlayLayout: max of all child sizes on both axes
// - FreeLayout: the full available space (children are manually positioned)
func (c *Container) Measure(availW, availH int) (int, int) {
	if len(c.children) == 0 {
		return availW, availH
	}

	switch c.layout.(type) {
	case *StackLayout:
		sl := c.layout.(*StackLayout)
		return c.measureStack(sl, availW, availH)
	case OverlayLayout:
		return c.measureOverlay(availW, availH)
	case FreeLayout:
		// FreeLayout children are manually positioned; the container
		// just takes whatever space is available.
		return availW, availH
	default:
		// Unknown layout: measure all children and take the maximum.
		return c.measureOverlay(availW, availH)
	}
}

// measureStack computes the preferred size for a StackLayout.
func (c *Container) measureStack(sl *StackLayout, availW, availH int) (int, int) {
	totalGap := sl.Gap * (len(c.children) - 1)
	if totalGap < 0 {
		totalGap = 0
	}

	sumMajor := 0
	maxMinor := 0

	for _, cw := range c.children {
		pw, ph := cw.Widget.Measure(availW, availH)

		if sl.Direction == Vertical {
			sumMajor += ph
			if pw > maxMinor {
				maxMinor = pw
			}
		} else {
			sumMajor += pw
			if ph > maxMinor {
				maxMinor = ph
			}
		}
	}

	if sl.Direction == Vertical {
		return min(maxMinor, availW), min(sumMajor+totalGap, availH)
	}
	return min(sumMajor+totalGap, availW), min(maxMinor, availH)
}

// measureOverlay computes the preferred size for an OverlayLayout.
func (c *Container) measureOverlay(availW, availH int) (int, int) {
	maxW, maxH := 0, 0
	for _, cw := range c.children {
		pw, ph := cw.Widget.Measure(availW, availH)
		if pw > maxW {
			maxW = pw
		}
		if ph > maxH {
			maxH = ph
		}
	}
	return min(maxW, availW), min(maxH, availH)
}
