package wm

import (
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/widget"
)

// FloatingWindow is a decorated, interactive surface that floats above
// other content in the window manager.
//
// A FloatingWindow has:
//   - A content widget (the actual UI inside the window)
//   - Decorations (border, title bar, close button) drawn around the content
//   - Interaction state (drag, resize, focus)
//   - Z-order position among other windows
//
// The window surface includes both the decoration and content areas.
// The decoration occupies the border rows/columns; the content is the inner area.
type FloatingWindow struct {
	// ID is the unique identifier for this window.
	ID string
	// Title is the text displayed in the window's title bar.
	Title string
	// Content is the widget rendered inside the window's content area.
	Content widget.Widget

	// Position (screen coordinates).
	x, y int
	// Size (including decorations).
	w, h int
	// Minimum size constraints.
	minW, minH int
	// Maximum size constraints (0 = unlimited).
	maxW, maxH int

	// decorated controls whether the window has a border and title bar.
	decorated bool
	// movable controls whether the window can be dragged.
	movable bool
	// resizable controls whether the window can be resized by dragging edges.
	resizable bool
	// closable controls whether the window has a close button.
	closable bool
	// modal controls whether the window blocks input to windows below.
	modal bool
	// visible controls whether the window is rendered.
	visible bool
	// state is the current window state (normal, minimized, etc.).
	state WindowState
	// focused indicates whether this window has keyboard focus.
	focused bool
	// focusable controls whether the window can receive focus and mouse input.
	focusable bool

	// Decoration configuration.
	borderStyle BorderStyle
	titleAlign  TextAlign

	// Content area (computed from w/h minus decorations).
	contentX, contentY int
	contentW, contentH int

	// Interaction state (transient, per-interaction).
	dragging     bool
	dragStartX   int
	dragStartY   int
	dragOffX     int
	dragOffY     int
	resizing     bool
	resizeEdge   Edge
	resizeStartW int
	resizeStartH int

	// surf is the window's rendering surface (created lazily by Render).
	surf *surface.Surface
	// contentSurf is the reusable content surface (prevents per-frame allocation).
	contentSurf *surface.Surface

	// contentDirty tracks whether the window's content needs re-rendering.
	// Position-only changes (drag) do not mark content dirty.
	contentDirty bool
}

// NewFloatingWindow creates a new floating window with the given dimensions
// and options.
//
// The window is visible by default. All options are applied before the
// content area is computed. The content area depends on border style:
//   - BorderNone: (0,1) with size (w, h-1)
//   - BorderLShape: (1,1) with size (w-1, h-2)
//   - Other borders: (1,1) with size (w-2, h-2)
//   - Undecorated: (0,0) with size (w, h)
//
// A minimum content size of 1×1 is enforced.
func NewFloatingWindow(id, title string, content widget.Widget, w, h int, opts ...WindowOption) *FloatingWindow {
	fw := &FloatingWindow{
		ID:           id,
		Title:        title,
		Content:      content,
		w:            w,
		h:            h,
		decorated:    true, // decorated by default
		movable:      true, // movable by default
		resizable:    true, // resizable by default
		closable:     true, // closable by default
		focusable:    true, // focusable by default
		visible:      true,
		state:        WindowNormal,
		contentDirty: true, // needs initial render
	}

	for _, opt := range opts {
		opt(fw)
	}

	// Clamp size to respect min/max bounds.
	ClampSize(fw)

	fw.computeContentArea()

	return fw
}

// computeContentArea calculates the content area based on the window size,
// decoration state, and border style.
//
// For decorated windows with BorderNone: content starts at column 0, row 1
// (after the title bar), with full width and height-1.
//
// For decorated windows with BorderLShape: content starts at column 1, row 1
// (after left border + title bar), with width-1 and height-2.
// Left column is the border, bottom row is the border.
//
// For decorated windows with border chars: content starts at column 1, row 1
// (inside the border), with width-2 and height-2.
//
// For undecorated windows: content fills the entire window.
func (fw *FloatingWindow) computeContentArea() {
	if fw.decorated {
		switch fw.borderStyle {
		case BorderNone:
			// No border chars: content starts at column 0, row 1 (after title bar).
			fw.contentX = 0
			fw.contentY = 1
			fw.contentW = fw.w
			fw.contentH = fw.h - 1
		case BorderLShape:
			// Left+bottom border: content starts at column 1, row 1.
			// Left column (x=0) is border, bottom row (y=h-1) is border.
			// No right border — content extends to the right edge.
			fw.contentX = 1
			fw.contentY = 1
			fw.contentW = fw.w - 1
			fw.contentH = fw.h - 2
		default:
			// Full border chars: content starts at column 1, row 1 (inside border).
			fw.contentX = 1
			fw.contentY = 1
			fw.contentW = fw.w - 2
			fw.contentH = fw.h - 2
		}
	} else {
		fw.contentX = 0
		fw.contentY = 0
		fw.contentW = fw.w
		fw.contentH = fw.h
	}

	// Ensure minimum content size of 1×1.
	if fw.contentW < 1 {
		fw.contentW = 1
	}
	if fw.contentH < 1 {
		fw.contentH = 1
	}
}

// ContentBounds returns the inner content area (excluding decorations)
// as a Rect in window-local coordinates.
func (fw *FloatingWindow) ContentBounds() surface.Rect {
	return surface.Rect{
		X: fw.contentX,
		Y: fw.contentY,
		W: fw.contentW,
		H: fw.contentH,
	}
}

// Position returns the window's screen coordinates.
func (fw *FloatingWindow) Position() (int, int) {
	return fw.x, fw.y
}

// HitTestEdge returns which edge or corner a screen coordinate hits.
// Returns EdgeNone if the coordinate is not on an edge or if the window
// is not decorated.
//
// For BorderNone: the top row is the title bar (for dragging), not a resize
// handle. Only bottom and side edges are detected for resize.
//
// For BorderLShape: only left and bottom edges are detected. The top row is
// the title bar, and the right side has no border (scrollbar area).
//
// For fully bordered windows: all edges and corners are detected.
func (fw *FloatingWindow) HitTestEdge(screenX, screenY int) Edge {
	if !fw.decorated {
		return EdgeNone
	}

	// Convert to window-local coordinates.
	lx := screenX - fw.x
	ly := screenY - fw.y

	// Must be within the window bounds.
	if lx < 0 || ly < 0 || lx >= fw.w || ly >= fw.h {
		return EdgeNone
	}

	if fw.borderStyle == BorderNone || fw.borderStyle == BorderLShape {
		// For BorderNone and BorderLShape: top row is the title bar (drag),
		// not a resize handle. Only detect edges on bottom and left sides.
		if ly == fw.h-1 {
			// Bottom edge + corners.
			if lx == 0 {
				return EdgeBottomLeft
			}
			if lx == fw.w-1 {
				return EdgeBottomRight
			}
			return EdgeBottom
		}
		if ly > 0 {
			// Left edge only (no right border for BorderNone/LShape).
			if lx == 0 {
				return EdgeLeft
			}
			if fw.borderStyle == BorderNone && lx == fw.w-1 {
				return EdgeRight
			}
		}
		return EdgeNone
	}

	// Fully bordered windows: all edges and corners.
	if lx == 0 && ly == 0 {
		return EdgeTopLeft
	}
	if lx == fw.w-1 && ly == 0 {
		return EdgeTopRight
	}
	if lx == 0 && ly == fw.h-1 {
		return EdgeBottomLeft
	}
	if lx == fw.w-1 && ly == fw.h-1 {
		return EdgeBottomRight
	}
	if ly == 0 {
		return EdgeTop
	}
	if ly == fw.h-1 {
		return EdgeBottom
	}
	if lx == 0 {
		return EdgeLeft
	}
	if lx == fw.w-1 {
		return EdgeRight
	}

	return EdgeNone
}

// ContainsPoint reports whether a screen coordinate is within this window.
func (fw *FloatingWindow) ContainsPoint(screenX, screenY int) bool {
	lx := screenX - fw.x
	ly := screenY - fw.y
	return lx >= 0 && ly >= 0 && lx < fw.w && ly < fw.h
}

// SetPosition moves the window to the given screen coordinates.
func (fw *FloatingWindow) SetPosition(x, y int) {
	fw.x = x
	fw.y = y
}

// SetSize resizes the window (clamped to min/max size) and recomputes
// the content area.
func (fw *FloatingWindow) SetSize(w, h int) {
	fw.w = w
	fw.h = h
	ClampSize(fw)
	fw.computeContentArea()
	fw.contentDirty = true
}

// ApplyResize adjusts the window size and position based on a drag edge
// and the delta from the last mouse position.
//
// For right/bottom edges: the window grows/shrinks.
// For left/top edges: the window grows/shrinks and its position shifts
// to keep the opposite edge anchored.
func (fw *FloatingWindow) ApplyResize(edge Edge, dx, dy int) {
	switch edge {
	case EdgeRight, EdgeTopRight, EdgeBottomRight:
		fw.w += dx
	case EdgeLeft, EdgeTopLeft, EdgeBottomLeft:
		fw.w -= dx
		fw.x += dx
	}
	switch edge {
	case EdgeBottom, EdgeBottomLeft, EdgeBottomRight:
		fw.h += dy
	case EdgeTop, EdgeTopLeft, EdgeTopRight:
		fw.h -= dy
		fw.y += dy
	}
	ClampSize(fw)
	fw.computeContentArea()
	fw.contentDirty = true
}

// ApplyMove adjusts the window position by the given drag delta.
func (fw *FloatingWindow) ApplyMove(dx, dy int) {
	fw.x += dx
	fw.y += dy
}

// Render draws the window (decorations + content) into the window's
// surface. It creates or resizes the window's surface as needed.
//
// Steps:
//  1. Create/resize the window's surface to (fw.w, fw.h)
//  2. Fill with transparent blanks (so uncovered areas show through)
//  3. If decorated, draw decorations (border, title bar, close button)
//  4. Create a child surface for the content area
//  5. Call fw.Content.Render(contentSurf, ctx)
func (fw *FloatingWindow) Render(ctx *widget.RenderContext) {
	// Always create/resize the window's surface.
	if fw.surf == nil {
		fw.surf = surface.NewSurface(fw.ID, fw.w, fw.h, cell.TransparentBlank())
		fw.contentDirty = true // new surface needs render
	} else {
		fw.surf.Resize(fw.w, fw.h)
	}

	if fw.contentDirty {
		// Clear to transparent blanks so uncovered areas show through.
		fw.surf.Buffer().Clear()

		// Clear previous frame's children to prevent accumulation.
		fw.surf.RemoveAllChildren()

		// Draw decorations if the window is decorated.
		if fw.decorated {
			RenderDecoration(fw, fw.surf, ctx.Theme)
		}

		// Create or reuse a child surface for the content area and render the content.
		if fw.Content != nil && fw.contentW > 0 && fw.contentH > 0 {
			// Reuse or create content surface
			if fw.contentSurf == nil || fw.contentSurf.Width() != fw.contentW || fw.contentSurf.Height() != fw.contentH {
				fw.contentSurf = surface.NewSurface(
					fw.ID+"-content",
					fw.contentW,
					fw.contentH,
					cell.TransparentBlank(),
				)
			}
			fw.contentSurf.Buffer().Clear() // Clear for fresh rendering
			fw.contentSurf.SetPosition(fw.contentX, fw.contentY)

			// Render the content widget with the appropriate focus state.
			contentCtx := ctx.WithFocus(fw.focused)
			fw.Content.Render(fw.contentSurf, contentCtx)

			// Make cells with default/NoColor background transparent so the window's
			// background fill shows through. Without this, NoColor cells are opaque
			// and overwrite the window's SubPaneBg during Blit compositing.
			makeNoColorBgTransparent(fw.contentSurf.Buffer())

			// Attach the content surface as a child of the window surface.
			fw.surf.AddChild(fw.contentSurf)
		}

		fw.contentDirty = false
	}
}

// Surface returns the window's rendering surface (populated after Render).
func (fw *FloatingWindow) Surface() *surface.Surface {
	return fw.surf
}

// Focus marks this window as having keyboard focus.
func (fw *FloatingWindow) Focus() {
	fw.focused = true
	fw.contentDirty = true // decoration colors change
}

// Unfocus removes keyboard focus from this window.
func (fw *FloatingWindow) Unfocus() {
	fw.focused = false
	fw.contentDirty = true // decoration colors change
}

// Focused reports whether this window has keyboard focus.
func (fw *FloatingWindow) Focused() bool { return fw.focused }

// SetVisible controls whether the window is rendered.
func (fw *FloatingWindow) SetVisible(v bool) { fw.visible = v }

// Visible reports whether the window is visible.
func (fw *FloatingWindow) Visible() bool { return fw.visible }

// Modal reports whether this is a modal window (blocks input to others).
func (fw *FloatingWindow) Modal() bool { return fw.modal }

// Decorated reports whether the window has border decorations.
func (fw *FloatingWindow) Decorated() bool { return fw.decorated }

// Movable reports whether the window can be dragged.
func (fw *FloatingWindow) Movable() bool { return fw.movable }

// Resizable reports whether the window can be resized.
func (fw *FloatingWindow) Resizable() bool { return fw.resizable }

// Closable reports whether the window has a close button.
func (fw *FloatingWindow) Closable() bool { return fw.closable }

// State returns the current window state.
func (fw *FloatingWindow) State() WindowState { return fw.state }

// Size returns the window's current dimensions (width, height).
func (fw *FloatingWindow) Size() (int, int) { return fw.w, fw.h }

// BorderStyle returns the window's border style.
func (fw *FloatingWindow) BorderStyle() BorderStyle { return fw.borderStyle }

// TitleAlign returns the title text alignment.
func (fw *FloatingWindow) TitleAlign() TextAlign { return fw.titleAlign }

// IsDragging reports whether the window is currently being dragged.
func (fw *FloatingWindow) IsDragging() bool { return fw.dragging }

// IsResizing reports whether the window is currently being resized.
func (fw *FloatingWindow) IsResizing() bool { return fw.resizing }

// SetDragging sets the window's drag state.
func (fw *FloatingWindow) SetDragging(d bool) { fw.dragging = d }

// SetDragStart records the starting position for a drag operation.
func (fw *FloatingWindow) SetDragStart(x, y int) {
	fw.dragStartX = x
	fw.dragStartY = y
	fw.dragOffX = x - fw.x
	fw.dragOffY = y - fw.y
}

// DragOffset returns the offset from the drag start to the window position.
func (fw *FloatingWindow) DragOffset() (int, int) {
	return fw.dragOffX, fw.dragOffY
}

// SetResizing sets the window's resize state and the edge being dragged.
func (fw *FloatingWindow) SetResizing(r bool, edge Edge) {
	fw.resizing = r
	fw.resizeEdge = edge
	fw.resizeStartW = fw.w
	fw.resizeStartH = fw.h
}

// ResizeEdge returns the edge being used for resizing.
func (fw *FloatingWindow) ResizeEdge() Edge { return fw.resizeEdge }

// ResizeStartSize returns the window size at the start of the resize.
func (fw *FloatingWindow) ResizeStartSize() (int, int) {
	return fw.resizeStartW, fw.resizeStartH
}

// IsOnTitleBar reports whether a screen coordinate is on the title bar
// (the top row of the window).
//
// For BorderNone: the entire top row (x=0 to x=w-1) is the title bar.
// For BorderLShape: the top row from x=1 to x=w-1 (left column is border).
// For fully bordered windows: the top row between border corners (x=1 to x=w-2).
func (fw *FloatingWindow) IsOnTitleBar(screenX, screenY int) bool {
	if !fw.decorated {
		return false
	}
	lx := screenX - fw.x
	ly := screenY - fw.y
	if ly != 0 {
		return false
	}
	switch fw.borderStyle {
	case BorderNone:
		// Entire top row is title bar (no border corners to exclude).
		return lx >= 0 && lx < fw.w
	case BorderLShape:
		// Title bar from x=1 to x=w-1 (left column x=0 is border).
		return lx >= 1 && lx < fw.w
	default:
		// With full borders: exclude corner cells.
		return lx > 0 && lx < fw.w-1
	}
}

// IsOnCloseButton reports whether a screen coordinate hits the close button
// area in the title bar.
//
// For BorderNone and BorderLShape: the close button occupies positions
// (w-3, w-2, w-1) — flush with right edge, aligned with scrollbar column.
// For fully bordered windows: the close button occupies positions (w-4, w-3, w-2).
func (fw *FloatingWindow) IsOnCloseButton(screenX, screenY int) bool {
	if !fw.closable || !fw.decorated {
		return false
	}
	lx := screenX - fw.x
	ly := screenY - fw.y
	if ly != 0 {
		return false
	}
	if fw.borderStyle == BorderNone || fw.borderStyle == BorderLShape {
		// Close button at w-3, w-2, w-1 (right edge, aligned with scrollbar).
		return lx >= fw.w-3 && lx <= fw.w-1
	}
	// With full borders: close button at w-4, w-3, w-2 (leaving w-1 for border).
	return lx >= fw.w-4 && lx <= fw.w-2
}

// MarkContentDirty flags the window's content as needing re-rendering.
// Call this when the window's appearance changes (resize, focus, etc.).
func (fw *FloatingWindow) MarkContentDirty() { fw.contentDirty = true }

// IsContentDirty reports whether the window's content needs re-rendering.
func (fw *FloatingWindow) IsContentDirty() bool { return fw.contentDirty }

// makeNoColorBgTransparent replaces blank cells with NoColor background with
// transparent cells. This ensures that content widgets that don't set explicit
// background colors don't overwrite the window's background fill during Blit
// compositing.
//
// Only BLANK cells (spaces and continuation cells) are made transparent.
// Text cells with NoColor BG must remain opaque so their characters are
// visible during Blit compositing — the window's body background is already
// painted on the parent surface, so NoColor BG cells compositing over it
// looks correct (default bg ≈ body bg).
func makeNoColorBgTransparent(buf *surface.CellBuffer) {
	cells := buf.Cells()
	for i := range cells {
		c := &cells[i]
		if c.BG.Type == cell.ColorDefault && !c.Transparent && c.IsBlank() {
			// Blank cell with no explicit background — make it transparent
			// so the window's background fill shows through.
			c.Transparent = true
		}
	}
}
