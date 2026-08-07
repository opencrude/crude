package wm

import (
	"crude/tui/surface"
	"crude/tui/theme"
)

// RenderContext carries rendering state through the window tree. It replaces
// the deleted legacy widget.RenderContext: Theme + focus + spin frame. Opacity
// and ClipStack were dead in the WM path (the compositor owns clip/z-order and
// nothing consumes per-window opacity).
type RenderContext struct {
	// Theme provides the current color scheme and styles.
	Theme *theme.Theme

	// Focused reports whether the window has keyboard focus.
	Focused bool

	// SpinFrame is the current animation frame counter (for spinners, blink).
	SpinFrame int
}

// NewRenderContext creates a default render context with the given theme.
func NewRenderContext(th *theme.Theme) *RenderContext {
	return &RenderContext{Theme: th}
}

// WithFocus returns a shallow copy of the context with the given focus state.
// The Theme pointer is shared with the original.
func (ctx *RenderContext) WithFocus(focused bool) *RenderContext {
	cp := *ctx
	cp.Focused = focused
	return &cp
}

// WindowContent is the render contract for floating-window bodies and the
// window-manager background. It is the WM-owned successor to the deleted
// widget.Widget: content renders into a surface, while the WM owns placement,
// z-order, and focus. ui.ComponentWidget / ui.WindowFrameWidget and
// ui.PopupContent implement it.
type WindowContent interface {
	// Render draws the content into the given surface.
	// The surface is pre-sized to the content's allocated space.
	Render(surf *surface.Surface, ctx *RenderContext)
}
