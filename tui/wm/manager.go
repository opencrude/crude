package wm

import (
	"crude/tui/rendering"
	"crude/tui/surface"

	tea "charm.land/bubbletea/v2"
)

// WindowManager manages all floating windows.
//
// Responsibilities:
// - Maintain z-ordered list of windows
// - Route keyboard input to the focused window
// - Route mouse input to the correct window (hit-testing)
// - Handle drag and resize interactions
// - Manage focus chain (Tab / Shift+Tab between windows)
// - Render all windows into the compositor's surface tree
//
// The WindowManager does NOT depend on the compositor package.
// It renders windows into surfaces; the compositor composites those surfaces.
type WindowManager struct {
	// windows holds all windows in creation order.
	windows []*FloatingWindow
	// zOrder holds windows sorted by z-order (bottom to top).
	zOrder []*FloatingWindow
	// focused is the currently focused window.
	focused *FloatingWindow

	// background is the main content widget rendered behind all windows.
	background WindowContent

	// Interaction state.
	activeDrag   *FloatingWindow
	activeResize *FloatingWindow
	lastMouseX   int
	lastMouseY   int
	needsRender  bool // set true when drag/resize changes position
}

// NewWindowManager creates a new window manager with no windows.
func NewWindowManager() *WindowManager {
	return &WindowManager{
		windows: make([]*FloatingWindow, 0),
		zOrder:  make([]*FloatingWindow, 0),
	}
}

// AddWindow adds a floating window to the manager.
// The window is placed at the top of the z-order and receives focus.
func (wm *WindowManager) AddWindow(fw *FloatingWindow) {
	wm.windows = append(wm.windows, fw)
	wm.zOrder = append(wm.zOrder, fw)
	if fw.focusable {
		wm.FocusWindow(fw.ID)
	}
}

// RemoveWindow removes a floating window by ID.
// Focus moves to the next window in z-order (or nil if no windows remain).
func (wm *WindowManager) RemoveWindow(id string) {
	// Remove from windows list.
	for i, fw := range wm.windows {
		if fw.ID == id {
			wm.windows = append(wm.windows[:i], wm.windows[i+1:]...)
			break
		}
	}

	// Remove from zOrder list.
	for i, fw := range wm.zOrder {
		if fw.ID == id {
			wm.zOrder = append(wm.zOrder[:i], wm.zOrder[i+1:]...)
			break
		}
	}

	// Clear interaction state if this window was being dragged/resized.
	if wm.activeDrag != nil && wm.activeDrag.ID == id {
		wm.activeDrag = nil
	}
	if wm.activeResize != nil && wm.activeResize.ID == id {
		wm.activeResize = nil
	}

	// Reassign focus.
	if wm.focused != nil && wm.focused.ID == id {
		wm.focused = nil
		if len(wm.zOrder) > 0 {
			wm.FocusWindow(wm.zOrder[len(wm.zOrder)-1].ID)
		}
	}
}

// Window returns a window by ID, or nil if not found.
func (wm *WindowManager) Window(id string) *FloatingWindow {
	for _, fw := range wm.windows {
		if fw.ID == id {
			return fw
		}
	}
	return nil
}

// Windows returns all windows in creation order.
func (wm *WindowManager) Windows() []*FloatingWindow {
	result := make([]*FloatingWindow, len(wm.windows))
	copy(result, wm.windows)
	return result
}

// TopWindow returns the topmost (focused) window, or nil if no windows exist.
func (wm *WindowManager) TopWindow() *FloatingWindow {
	if len(wm.zOrder) == 0 {
		return nil
	}
	return wm.zOrder[len(wm.zOrder)-1]
}

// FocusWindow brings a window to the top of the z-order and gives it focus.
func (wm *WindowManager) FocusWindow(id string) {
	// Unfocus the previously focused window.
	if wm.focused != nil {
		wm.focused.Unfocus()
	}

	// Find and move the window to the top of z-order.
	for i, fw := range wm.zOrder {
		if fw.ID == id {
			// Remove from current position.
			wm.zOrder = append(wm.zOrder[:i], wm.zOrder[i+1:]...)
			// Add to top.
			wm.zOrder = append(wm.zOrder, fw)
			wm.focused = fw
			fw.Focus()
			return
		}
	}
}

// FocusNext moves focus to the next window in z-order (cyclic).
func (wm *WindowManager) FocusNext() {
	if len(wm.zOrder) == 0 {
		return
	}

	// Find the current focused window's index.
	currentIdx := -1
	for i, fw := range wm.zOrder {
		if wm.focused != nil && fw.ID == wm.focused.ID {
			currentIdx = i
			break
		}
	}

	// Move to next (cyclic).
	nextIdx := currentIdx + 1
	if nextIdx >= len(wm.zOrder) {
		nextIdx = 0
	}

	wm.FocusWindow(wm.zOrder[nextIdx].ID)
}

// FocusPrev moves focus to the previous window in z-order (cyclic).
func (wm *WindowManager) FocusPrev() {
	if len(wm.zOrder) == 0 {
		return
	}

	// Find the current focused window's index.
	currentIdx := -1
	for i, fw := range wm.zOrder {
		if wm.focused != nil && fw.ID == wm.focused.ID {
			currentIdx = i
			break
		}
	}

	// Move to previous (cyclic).
	prevIdx := currentIdx - 1
	if prevIdx < 0 {
		prevIdx = len(wm.zOrder) - 1
	}

	wm.FocusWindow(wm.zOrder[prevIdx].ID)
}

// BringToTop moves a window to the top of the z-order.
func (wm *WindowManager) BringToTop(id string) {
	for i, fw := range wm.zOrder {
		if fw.ID == id {
			wm.zOrder = append(wm.zOrder[:i], wm.zOrder[i+1:]...)
			wm.zOrder = append(wm.zOrder, fw)
			return
		}
	}
}

// SendToBottom moves a window to the bottom of the z-order.
func (wm *WindowManager) SendToBottom(id string) {
	for i, fw := range wm.zOrder {
		if fw.ID == id {
			wm.zOrder = append(wm.zOrder[:i], wm.zOrder[i+1:]...)
			wm.zOrder = append([]*FloatingWindow{fw}, wm.zOrder...)
			return
		}
	}
}

// SetBackground sets the main content widget that renders behind all windows.
func (wm *WindowManager) SetBackground(w WindowContent) {
	wm.background = w
}

// Focused returns the currently focused window, or nil.
func (wm *WindowManager) Focused() *FloatingWindow {
	return wm.focused
}

// ClearFocus drops window focus without reassigning it to another window. The
// OverlayHost uses it to restore "no window focused" when the focus prior to an
// overlay was the shell (editor/tree/chat), instead of snapping focus to
// whatever is on top of the z-order.
func (wm *WindowManager) ClearFocus() {
	if wm.focused != nil {
		wm.focused.Unfocus()
		wm.focused = nil
	}
}

// IsDragging reports whether a drag or resize interaction is in progress.
func (wm *WindowManager) IsDragging() bool {
	return wm.activeDrag != nil || wm.activeResize != nil
}

// NeedsRender reports whether a drag/resize moved a window since the last render.
func (wm *WindowManager) NeedsRender() bool { return wm.needsRender }

// ClearNeedsRender resets the needs-render flag after rendering.
func (wm *WindowManager) ClearNeedsRender() { wm.needsRender = false }

// HandleMouse processes a mouse click event and routes it to the correct window.
// Returns the window that consumed the event (if any).
//
// This handles:
// - Click on close button → returns the window (caller should remove it)
// - Click on title bar → starts drag, focuses window
// - Click on edge → starts resize
// - Click inside window → focuses window
// - Click outside all windows → unfocuses all
func (wm *WindowManager) HandleMouse(msg tea.MouseMsg) *FloatingWindow {
	m := msg.Mouse()

	// If we have an active drag or resize, don't start a new one;
	// the motion handler continues the interaction.
	// However, still check if the click is on the close button of the
	// actively dragged window — users expect to close by clicking the X.
	if wm.activeDrag != nil || wm.activeResize != nil {
		if wm.activeDrag != nil && wm.activeDrag.Closable() && wm.activeDrag.IsOnCloseButton(m.X, m.Y) {
			return wm.activeDrag // Let caller handle close
		}
		return wm.focused
	}

	// Iterate windows top-to-bottom (reverse zOrder) to find the hit.
	for i := len(wm.zOrder) - 1; i >= 0; i-- {
		fw := wm.zOrder[i]
		if !fw.Visible() || !fw.focusable {
			continue
		}

		if fw.ContainsPoint(m.X, m.Y) {
			// If a modal window is above this one, skip non-modal windows.
			if wm.isBlockedByModal(fw) {
				// But if the click is on the modal window itself, allow it.
				continue
			}

			// Focus this window.
			wm.FocusWindow(fw.ID)

			// Check close button.
			if fw.IsOnCloseButton(m.X, m.Y) {
				return fw // Caller should handle removal.
			}

			// Check title bar for drag (before edge check — title bar is
			// a more specific hit target than the resize edge).
			if fw.Movable() && fw.IsOnTitleBar(m.X, m.Y) {
				wm.activeDrag = fw
				fw.SetDragging(true)
				fw.SetDragStart(m.X, m.Y)
				wm.lastMouseX = m.X
				wm.lastMouseY = m.Y
				return fw
			}

			// Check edge for resize.
			if fw.Resizable() {
				edge := fw.HitTestEdge(m.X, m.Y)
				if edge != EdgeNone {
					wm.activeResize = fw
					fw.SetResizing(true, edge)
					wm.lastMouseX = m.X
					wm.lastMouseY = m.Y
					return fw
				}
			}

			// Click inside the window content area.
			return fw
		}
	}

	// Click outside all windows — unfocus.
	if wm.focused != nil {
		wm.focused.Unfocus()
		wm.focused = nil
	}

	return nil
}

// HandleMouseMotion handles mouse motion for drag/resize interactions.
// Returns the window being interacted with (if any).
func (wm *WindowManager) HandleMouseMotion(msg tea.MouseMsg) *FloatingWindow {
	m := msg.Mouse()

	// Handle active drag.
	if wm.activeDrag != nil {
		// In all-motion mode (DECSET 1003), motion events arrive even after
		// button release. If no button is pressed, the drag is over.
		if m.Button == tea.MouseNone {
			wm.activeDrag.SetDragging(false)
			wm.activeDrag = nil
			wm.needsRender = true
			return nil
		}
		dx := m.X - wm.lastMouseX
		dy := m.Y - wm.lastMouseY
		if dx != 0 || dy != 0 {
			wm.activeDrag.ApplyMove(dx, dy)
			wm.needsRender = true
		}
		wm.lastMouseX = m.X
		wm.lastMouseY = m.Y
		return wm.activeDrag
	}

	// Handle active resize.
	if wm.activeResize != nil {
		if m.Button == tea.MouseNone {
			wm.activeResize.SetResizing(false, EdgeNone)
			wm.activeResize = nil
			wm.needsRender = true
			return nil
		}
		dx := m.X - wm.lastMouseX
		dy := m.Y - wm.lastMouseY
		if dx != 0 || dy != 0 {
			wm.activeResize.ApplyResize(wm.activeResize.ResizeEdge(), dx, dy)
			wm.needsRender = true
		}
		wm.lastMouseX = m.X
		wm.lastMouseY = m.Y
		return wm.activeResize
	}

	return nil
}

// HandleMouseRelease ends drag/resize interactions.
// Returns the window that was released (if any).
func (wm *WindowManager) HandleMouseRelease(msg tea.MouseMsg) *FloatingWindow {
	var released *FloatingWindow
	if wm.activeDrag != nil {
		released = wm.activeDrag
		wm.activeDrag.SetDragging(false)
		wm.activeDrag = nil
	}
	if wm.activeResize != nil {
		released = wm.activeResize
		wm.activeResize.SetResizing(false, EdgeNone)
		wm.activeResize = nil
	}
	return released
}

// HandleMouseWheel handles a mouse wheel event by hit-testing against
// floating windows top-to-bottom. Returns the window under the cursor,
// or nil if no window was hit.
func (wm *WindowManager) HandleMouseWheel(msg tea.MouseWheelMsg) *FloatingWindow {
	// Iterate windows top-to-bottom (reverse zOrder).
	for i := len(wm.zOrder) - 1; i >= 0; i-- {
		fw := wm.zOrder[i]
		if !fw.Visible() || !fw.focusable {
			continue
		}
		if fw.ContainsPoint(msg.X, msg.Y) {
			return fw
		}
	}
	return nil
}

// isBlockedByModal reports whether a window is blocked by a modal window
// above it in the z-order. A window is blocked if there is any visible
// modal window with a higher z-order position.
func (wm *WindowManager) isBlockedByModal(fw *FloatingWindow) bool {
	fwIdx := -1
	for i, w := range wm.zOrder {
		if w.ID == fw.ID {
			fwIdx = i
			break
		}
	}
	if fwIdx < 0 {
		return false
	}

	// Check if any window above (higher index) this one is modal and visible.
	for i := fwIdx + 1; i < len(wm.zOrder); i++ {
		above := wm.zOrder[i]
		if above.Visible() && above.Modal() {
			return true
		}
	}
	return false
}

// Render renders the background and all floating windows into the
// compositor's root surface.
//
// The background widget is rendered first, then each floating window
// is rendered as a child surface of the root, positioned at its (x, y).
func (wm *WindowManager) Render(rootSurf *surface.Surface, ctx *RenderContext) {
	// Render the background widget into the root surface.
	if wm.background != nil {
		wm.background.Render(rootSurf, ctx)
	}

	// Render each visible window in z-order (bottom to top).
	for i, fw := range wm.zOrder {
		if !fw.Visible() {
			continue
		}

		// Render the window into its own surface.
		fw.Render(ctx)

		// Get the window's surface and attach it to the root.
		winSurf := fw.Surface()
		if winSurf == nil {
			continue
		}

		x, y := fw.Position()
		rendering.Log("WindowManager.Render fw=%q screenPos=(%d,%d) surfW=%d surfH=%d screenCols: left=%d right=%d zOrder=%d",
			fw.ID, x, y, fw.w, fw.h, x, x+fw.w-1, i)

		// Position the window surface at the window's screen coordinates.
		winSurf.SetPosition(x, y)

		// Set z-order to match the window's position in the stack.
		// Offset by 10 so floating windows are always above the persistent
		// layer surfaces (main-content=0, sidebar=1, statusbar=2).
		winSurf.SetZOrder(i + 10)

		// Add the window surface as a child of the root surface.
		rootSurf.AddChild(winSurf)
	}
}
