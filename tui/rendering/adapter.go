package rendering

import (
	"crude/tui/surface"
	"crude/tui/widget"
)

// ViewFunc is a function that returns a string view (like Bubble Tea's View()).
type ViewFunc func() string

// ANSIAdapter wraps a legacy View()-returning function into the Widget interface.
// It calls the function to get an ANSI string, then parses it and writes cells
// into the surface.
//
// This enables existing widgets that return ANSI strings to work within the
// compositing engine without being rewritten. Widgets can be migrated to
// direct cell-level rendering one by one.
//
// ANSIAdapter satisfies the widget.Widget interface via structural typing.
type ANSIAdapter struct {
	viewFn ViewFunc
}

// NewANSIAdapter creates an adapter from a View()-returning function.
func NewANSIAdapter(viewFn ViewFunc) *ANSIAdapter {
	return &ANSIAdapter{viewFn: viewFn}
}

// Render implements Widget. Calls the view function, parses the ANSI string,
// and writes cells into the surface.  Blank cells with NoColor background are
// filled with SubPaneBg so makeNoColorBgTransparent cannot create transparent
// holes that leak background content through the floating window.
func (a *ANSIAdapter) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	if a.viewFn == nil {
		return
	}
	view := a.viewFn()
	RenderToSurface(surf, view)
	if ctx != nil && ctx.Theme != nil {
		bg := ThemeColorToCell(ctx.Theme.Colors.SubPaneBg)
		buf := surf.Buffer()
		for i := range buf.Cells() {
			c := &buf.Cells()[i]
			if c.BG.IsDefault() && c.IsBlank() {
				c.BG = bg
			}
		}
	}
}

// Measure implements Widget. Returns the surface dimensions as the preferred size.
func (a *ANSIAdapter) Measure(availW, availH int) (int, int) {
	return availW, availH
}

// ModelAdapter wraps a Bubble Tea model into the Widget interface.
// It uses the model's View() method to get the ANSI string.
//
// ModelAdapter satisfies the widget.Widget interface via structural typing.
type ModelAdapter struct {
	Model interface {
		View() string
	}
}

// NewModelAdapter creates an adapter from a Bubble Tea model.
func NewModelAdapter(model interface{ View() string }) *ModelAdapter {
	return &ModelAdapter{Model: model}
}

// Render implements Widget.
func (m *ModelAdapter) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	view := m.Model.View()
	RenderToSurface(surf, view)
	if ctx != nil && ctx.Theme != nil {
		bg := ThemeColorToCell(ctx.Theme.Colors.SubPaneBg)
		buf := surf.Buffer()
		for i := range buf.Cells() {
			c := &buf.Cells()[i]
			if c.BG.IsDefault() && c.IsBlank() {
				c.BG = bg
			}
		}
	}
}

// Measure implements Widget.
func (m *ModelAdapter) Measure(availW, availH int) (int, int) {
	return availW, availH
}
