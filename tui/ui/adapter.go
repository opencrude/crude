package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/widget"
)

// LegacyWidget is a temporary bridge from the old widget.Widget interface
// (Render/Measure only) into the Component tree.
//
// Every use must be listed in docs/tui/bypass_manifest.json control_bypasses
// and deleted when the consumer migrates. New product code must not depend
// on this adapter.
type LegacyWidget struct {
	Base
	inner widget.Widget
}

// NewLegacyWidget wraps a widget.Widget.
func NewLegacyWidget(id ComponentID, inner widget.Widget) *LegacyWidget {
	return &LegacyWidget{
		Base:  NewBase(id),
		inner: inner,
	}
}

// Inner returns the wrapped widget.
func (l *LegacyWidget) Inner() widget.Widget { return l.inner }

func (l *LegacyWidget) Measure(_ LayoutContext, cs Constraints) Size {
	if l.inner == nil {
		return cs.ClampSize(Size{})
	}
	w, h := l.inner.Measure(cs.MaxW, cs.MaxH)
	return cs.ClampSize(Size{W: w, H: h})
}

func (l *LegacyWidget) Layout(_ LayoutContext, r Rect) {
	l.SetBounds(r)
}

func (l *LegacyWidget) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	if l.inner == nil {
		return
	}
	r := l.Bounds()
	if r.Empty() {
		return
	}
	surf := surface.NewSurface(string(l.ID())+"-legacy", r.W, r.H, cell.TransparentBlank())
	rctx := &widget.RenderContext{
		Theme:     ctx.Theme,
		Focused:   ctx.Focused,
		SpinFrame: ctx.Frame,
	}
	l.inner.Render(surf, rctx)
	surface.Blit(buf, originX+r.X, originY+r.Y, surf.Buffer(), nil)
}

// Handle: legacy widgets are paint-only; events must be routed by the screen.
func (l *LegacyWidget) Handle(_ EventContext, _ tea.Msg) EventResult {
	return EventResult{}
}

func (l *LegacyWidget) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: string(l.ID()) + " (legacy)"}
}

// ANSIViewFunc adapts a View() string function into a Component via ANSI parse.
// Temporary bridge only.
type ANSIViewFunc struct {
	Base
	view func(w, h int) string
}

// NewANSIViewFunc wraps an ANSI-producing view function.
func NewANSIViewFunc(id ComponentID, view func(w, h int) string) *ANSIViewFunc {
	return &ANSIViewFunc{Base: NewBase(id), view: view}
}

func (a *ANSIViewFunc) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: cs.MaxH})
}

func (a *ANSIViewFunc) Layout(_ LayoutContext, r Rect) {
	a.SetBounds(r)
}

func (a *ANSIViewFunc) Paint(buf *surface.CellBuffer, originX, originY int, _ PaintContext) {
	if a.view == nil {
		return
	}
	r := a.Bounds()
	if r.Empty() {
		return
	}
	surf := surface.NewSurface(string(a.ID())+"-ansi", r.W, r.H, cell.TransparentBlank())
	// Lazy import path: rendering.RenderToSurface
	renderANSIToSurface(surf, a.view(r.W, r.H))
	surface.Blit(buf, originX+r.X, originY+r.Y, surf.Buffer(), nil)
}

func (a *ANSIViewFunc) Handle(_ EventContext, _ tea.Msg) EventResult {
	return EventResult{}
}
