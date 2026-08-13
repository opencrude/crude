package wm

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// FloatingWindow is an interactive surface that floats above other content.
//
// A FloatingWindow has:
// - A content widget (the actual UI inside the window)
// - Frame interaction regions (title bar, close button, resize edges)
// - Interaction state (drag, resize, focus)
// - Z-order position among other windows
//
// Visual chrome is owned by ui.WindowFrame and is rendered as part of Content.
// The WM owns only placement and interaction; Content fills the complete surface.
type FloatingWindow struct {
	// ID is the unique identifier for this window.
	ID string
	// Title is the text displayed in the window's title bar.
	Title string
	// Content is the widget rendered inside the window's content area.
	Content WindowContent

	// Position (screen coordinates).
	x, y int
	// Size of the complete WindowFrame content surface.
	w, h int
	// Minimum size constraints.
	minW, minH int
	// Maximum size constraints (0 = unlimited).
	maxW, maxH int

	// titleActionW reserves cells left of close for a content title action.
	titleActionW int
	// decorated controls whether standard WindowFrame interaction regions are active.
	// It does not cause the WM to paint chrome.
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

	// Content area. WindowFrame owns its own inner chrome/content layout, so the
	// WM content area is always the complete floating-window surface.
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

	// clickOutside defines what happens when the user clicks outside
	// this window. Default is CloseRoute (close + route click through).
	clickOutside ClickOutsideBehavior

	// contentDirty tracks whether the window's content needs re-rendering.
	// Position-only changes (drag) do not mark content dirty.
	contentDirty bool
}

// NewFloatingWindow creates a new floating window with the given dimensions
// and options.
//
// The window is visible and has standard frame interaction regions by default.
// Content is expected to include ui.WindowFrame when visual chrome is wanted.
func NewFloatingWindow(id, title string, content WindowContent, w, h int, opts ...WindowOption) *FloatingWindow {
	fw := &FloatingWindow{
		ID:           id,
		Title:        title,
		Content:      content,
		w:            w,
		h:            h,
		decorated:    true, // standard frame interactions by default
		movable:      true, // movable by default
		resizable:    true, // resizable by default
		closable:     true, // closable by default
		focusable:    true, // focusable by default
		visible:      true,
		state:        WindowNormal,
		contentDirty: true,                   // needs initial render
		clickOutside: ClickOutsideCloseRoute, // default: close + route
	}

	for _, opt := range opts {
		opt(fw)
	}

	// Clamp size to respect min/max bounds.
	ClampSize(fw)

	fw.computeContentArea()

	return fw
}

// computeContentArea assigns the complete window to its content. Visual frame
// insets are calculated once by ui.WindowFrame, not duplicated by the WM.
func (fw *FloatingWindow) computeContentArea() {
	fw.contentX = 0
	fw.contentY = 0
	fw.contentW = fw.w
	fw.contentH = fw.h

	// Ensure minimum content size of 1×1.
	if fw.contentW < 1 {
		fw.contentW = 1
	}
	if fw.contentH < 1 {
		fw.contentH = 1
	}

	rendering.Log("computeContentArea fw=%q x=%d y=%d w=%d h=%d contentX=%d contentY=%d contentW=%d contentH=%d framed=%v",
		fw.ID, fw.x, fw.y, fw.w, fw.h, fw.contentX, fw.contentY, fw.contentW, fw.contentH, fw.decorated)
}

// ContentBounds returns the full content surface in window-local coordinates.
// ui.WindowFrame owns its inner body bounds.
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

// HitTestEdge returns which standard WindowFrame edge or corner a screen
// coordinate hits. Visual border style is irrelevant to WM interaction.
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

	// WindowFrame uses a complete one-cell outer frame.
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
// SetContent replaces the content widget and forces a re-render. Used
// when a popup's content changes (e.g. focus shift in a confirm
// dialog) without needing to close and reopen the window.
func (fw *FloatingWindow) SetContent(c WindowContent) {
	fw.Content = c
	fw.contentDirty = true
}

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

// Render draws the window content into the window's surface. Visual chrome is
// part of Content (normally ui.WindowFrame); WM never paints decorations.
//
// Steps:
//  1. Create/resize the window's surface to (fw.w, fw.h)
//  2. Fill with transparent blanks (so uncovered areas show through)
//  3. Create a child surface covering the complete window
//  4. Call fw.Content.Render(contentSurf, ctx)
func (fw *FloatingWindow) Render(ctx *RenderContext) {
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

		rendering.Log("Render fw=%q screenPos=(%d,%d) screenContent: colStart=%d colEnd=%d contentW=%d contentH=%d focused=%v",
			fw.ID, fw.x, fw.y, fw.x+fw.contentX, fw.x+fw.contentX+fw.contentW-1, fw.contentW, fw.contentH, fw.focused)

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
	fw.contentDirty = true // WindowFrame focus colors change
}

// Unfocus removes keyboard focus from this window.
func (fw *FloatingWindow) Unfocus() {
	fw.focused = false
	fw.contentDirty = true // WindowFrame focus colors change
}

// Focused reports whether this window has keyboard focus.
func (fw *FloatingWindow) Focused() bool { return fw.focused }

// SetVisible controls whether the window is rendered.
func (fw *FloatingWindow) SetVisible(v bool) { fw.visible = v }

// Visible reports whether the window is visible.
func (fw *FloatingWindow) Visible() bool { return fw.visible }

// Modal reports whether this is a modal window (blocks input to others).
func (fw *FloatingWindow) Modal() bool { return fw.modal }

// Decorated reports whether standard frame interaction regions are active.
// Visual chrome is always supplied by Content.
func (fw *FloatingWindow) Decorated() bool { return fw.decorated }

// Movable reports whether the window can be dragged.
func (fw *FloatingWindow) Movable() bool { return fw.movable }

// Resizable reports whether the window can be resized.
func (fw *FloatingWindow) Resizable() bool { return fw.resizable }

// Closable reports whether the window has a close button.
func (fw *FloatingWindow) Closable() bool { return fw.closable }

// ClickOutsideBehavior returns what happens when the user clicks outside this window.
func (fw *FloatingWindow) ClickOutsideBehavior() ClickOutsideBehavior { return fw.clickOutside }

// SetClickOutsideBehavior sets the click-outside behavior for this window.
func (fw *FloatingWindow) SetClickOutsideBehavior(b ClickOutsideBehavior) { fw.clickOutside = b }

// Focusable reports whether the window can receive focus and mouse input.
func (fw *FloatingWindow) Focusable() bool { return fw.focusable }

// State returns the current window state.
func (fw *FloatingWindow) State() WindowState { return fw.state }

// Size returns the window's current dimensions (width, height).
func (fw *FloatingWindow) Size() (int, int) { return fw.w, fw.h }

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

// IsOnTitleBar reports whether a screen coordinate is on WindowFrame's title
// border. Corner cells remain resize targets; the close region is checked first.
func (fw *FloatingWindow) IsOnTitleBar(screenX, screenY int) bool {
	if !fw.decorated {
		return false
	}
	lx := screenX - fw.x
	ly := screenY - fw.y
	if ly != 0 {
		return false
	}
	return lx > 0 && lx < fw.w-1
}

// IsOnCloseButton reports whether a screen coordinate hits the close button
// area in the title bar.
//
// WindowFrame reserves the final three cells of the top border for close.
func (fw *FloatingWindow) IsOnCloseButton(screenX, screenY int) bool {
	if !fw.closable || !fw.decorated {
		return false
	}
	lx := screenX - fw.x
	ly := screenY - fw.y
	if ly != 0 {
		return false
	}
	return lx >= fw.w-3 && lx < fw.w
}

// SetTitleActionWidth reserves w cells left of the close button for a
// content-owned title-bar action (WindowFrame.SetTitleAction). Clicks there
// must dispatch to the content instead of starting a title drag.
func (fw *FloatingWindow) SetTitleActionWidth(w int) {
	if w < 0 {
		w = 0
	}
	fw.titleActionW = w
}

// IsOnTitleAction reports whether a screen coordinate hits the reserved
// title-action cells.
func (fw *FloatingWindow) IsOnTitleAction(screenX, screenY int) bool {
	if fw.titleActionW <= 0 || !fw.decorated {
		return false
	}
	lx := screenX - fw.x
	ly := screenY - fw.y
	if ly != 0 {
		return false
	}
	end := fw.w
	if fw.closable {
		end = fw.w - 3
	}
	return lx >= end-fw.titleActionW && lx < end
}

// MarkContentDirty flags the window's content as needing re-rendering.
// Call this when the window's appearance changes (resize, focus, etc.).
func (fw *FloatingWindow) MarkContentDirty() { fw.contentDirty = true }

// IsContentDirty reports whether the window's content needs re-rendering.
func (fw *FloatingWindow) IsContentDirty() bool { return fw.contentDirty }
