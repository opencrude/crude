package widget

import (
	"crude/tui/surface"
	"crude/tui/theme"
)

// Widget is the interface for all renderable components.
//
// Widgets render into a Surface (cell buffer) rather than returning
// ANSI strings. This enables:
//   - Compositing at the cell level
//   - Overlapping widgets
//   - Transparency
//   - Proper clipping
//   - Damage tracking
//
// Event handling is managed separately through the existing Bubble Tea
// model system. The Widget interface is purely for rendering/compositing.
type Widget interface {
	// Render draws the widget into the given surface.
	// The surface is pre-sized to the widget's allocated space.
	Render(surf *surface.Surface, ctx *RenderContext)

	// Measure reports the preferred size given available space.
	// availW, availH: the maximum space available.
	// Returns: (preferredWidth, preferredHeight).
	//
	// This is used by the layout engine to allocate space.
	Measure(availW, availH int) (prefW, prefH int)
}

// RenderContext carries rendering state through the widget tree.
//
// It is passed to every Widget.Render() call. Widgets can modify
// the context for their children (e.g., push a clip rect, change
// opacity) and must restore it when done.
type RenderContext struct {
	// Theme provides the current color scheme and styles.
	Theme *theme.Theme

	// ClipStack provides hierarchical scissor clipping.
	// Created once per frame and shared across all widgets.
	ClipStack *surface.ClipStack

	// Opacity is the current opacity multiplier (0.0–1.0).
	// Widgets should factor this into their rendering; for example,
	// blending their background/foreground colors accordingly.
	Opacity float32

	// Focused reports whether this widget has keyboard focus.
	Focused bool

	// SpinFrame is the current animation frame counter (for spinners, blink).
	SpinFrame int
}

// NewRenderContext creates a default render context with the given theme.
// The ClipStack is initialized fresh. Opacity defaults to 1.0.
func NewRenderContext(theme *theme.Theme) *RenderContext {
	return &RenderContext{
		Theme:     theme,
		ClipStack: surface.NewClipStack(),
		Opacity:   1.0,
		Focused:   false,
		SpinFrame: 0,
	}
}

// PushClip pushes a clip rect onto the ClipStack and returns a pop function.
// The new active clip is the intersection of the pushed rect and the previous active clip.
//
// Usage:
//
//	defer ctx.PushClip(r)()
func (ctx *RenderContext) PushClip(r surface.Rect) func() {
	ctx.ClipStack.Push(r)
	return ctx.ClipStack.Pop
}

// WithOpacity returns a shallow copy of the context with modified opacity.
// The new opacity is the product of the current opacity and the given factor.
// The Theme and ClipStack pointers are shared with the original.
func (ctx *RenderContext) WithOpacity(factor float32) *RenderContext {
	cp := *ctx
	cp.Opacity = cp.Opacity * factor
	return &cp
}

// WithFocus returns a shallow copy of the context with the given focus state.
// The Theme and ClipStack pointers are shared with the original.
func (ctx *RenderContext) WithFocus(focused bool) *RenderContext {
	cp := *ctx
	cp.Focused = focused
	return &cp
}

// WithSpinFrame returns a shallow copy of the context with the given spin frame.
// The Theme and ClipStack pointers are shared with the original.
func (ctx *RenderContext) WithSpinFrame(frame int) *RenderContext {
	cp := *ctx
	cp.SpinFrame = frame
	return &cp
}

// BaseWidget provides default no-op implementations for optional Widget methods.
// Embed BaseWidget in a struct to satisfy the Widget interface with sensible defaults,
// then override only the methods you need.
//
// The default Measure returns the full available space, indicating the widget
// is willing to fill whatever space it is given.
type BaseWidget struct{}

// Measure returns the full available space as the preferred size.
// This is the default for widgets that are willing to fill their container.
func (BaseWidget) Measure(availW, availH int) (int, int) {
	return availW, availH
}
