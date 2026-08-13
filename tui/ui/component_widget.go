package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/wm"
)

// ComponentWidget bridges a Component tree into wm.WindowContent so product
// FloatingWindows can host WindowFrame and other ui components.
//
// Keyboard/mouse for product dialogs may still be routed by viewmodel until
// full Host event cutover; this adapter is primarily the paint path.
type ComponentWidget struct {
	host    *Host
	preferW int
	preferH int
}

// NewComponentWidget wraps root for use as FloatingWindow content.
func NewComponentWidget(th *theme.Theme, root Component, preferW, preferH int) *ComponentWidget {
	h := NewHost(th, root)
	if root != nil {
		applyTokenSet(root, h.Tokens())
		h.RegisterTree(root)
	}
	return &ComponentWidget{host: h, preferW: preferW, preferH: preferH}
}

// Host returns the underlying host (focus, theme).
func (c *ComponentWidget) Host() *Host { return c.host }

// Root returns the root component.
func (c *ComponentWidget) Root() Component {
	if c.host == nil {
		return nil
	}
	return c.host.Root()
}

// SetRoot replaces the component tree.
func (c *ComponentWidget) SetRoot(root Component) {
	if c.host == nil {
		return
	}
	c.host.SetRoot(root)
	if root != nil {
		applyTokenSet(root, c.host.Tokens())
		c.host.RegisterTree(root)
	}
}

// Focus focuses a component by ID.
func (c *ComponentWidget) Focus(id ComponentID) {
	if c.host != nil {
		c.host.Focus(id)
	}
}

// Measure reports the preferred size (preferW/preferH clamped to available).
func (c *ComponentWidget) Measure(availW, availH int) (int, int) {
	w, h := c.preferW, c.preferH
	if w <= 0 {
		w = availW
	}
	if h <= 0 {
		h = availH
	}
	if w > availW {
		w = availW
	}
	if h > availH {
		h = availH
	}
	return w, h
}

// Render paints the component tree into the surface buffer.
func (c *ComponentWidget) Render(surf *surface.Surface, ctx *wm.RenderContext) {
	if c == nil || c.host == nil || surf == nil {
		return
	}
	w, h := surf.Width(), surf.Height()
	if w < 1 || h < 1 {
		return
	}
	if ctx != nil && ctx.Theme != nil {
		c.host.SetTheme(ctx.Theme)
		applyTokenSet(c.host.Root(), c.host.Tokens())
	}
	c.host.Resize(w, h)
	c.host.Layout()
	// Clear to popup background so transparent cells do not punch holes.
	bg := cell.NoColor
	if c.host.Tokens() != nil {
		bg = c.host.Tokens().Color(ColorPopupBg)
	}
	buf := surf.Buffer()
	if !bg.IsDefault() {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				buf.Set(x, y, cell.BlankCell(bg))
			}
		}
	} else {
		buf.Clear()
	}
	// Host.Paint walks Parent children; WindowFrame chrome + Stack body paint.
	c.host.Paint(buf)
}

type tokenConsumer interface {
	SetTokens(*TokenSet)
}

func applyTokenSet(c Component, tokens *TokenSet) {
	if c == nil {
		return
	}
	if consumer, ok := c.(tokenConsumer); ok {
		consumer.SetTokens(tokens)
	}
	if parent, ok := c.(Parent); ok {
		for _, child := range parent.Children() {
			applyTokenSet(child, tokens)
		}
	}
}

// WindowEventTarget is implemented by legacy window bodies that still own
// their internal event loop while their visual frame is supplied by WindowFrame.
type WindowEventTarget interface {
	HandleEvent(tea.Msg) tea.Cmd
}

// WindowFrameWidget is the single bridge between a FloatingWindow and unified
// WindowFrame chrome. It paints a component frame over the full WM surface and,
// for legacy bodies, translates events into the frame's content rectangle.
type WindowFrameWidget struct {
	*ComponentWidget
	frame       *WindowFrame
	eventTarget WindowEventTarget
}

// NewWindowFrameWidget builds a framed widget suitable for FloatingWindow
// Content. body is laid out inside WindowFrame; eventTarget may be nil when the
// body is a native component or events are routed by the screen.
func NewWindowFrameWidget(
	th *theme.Theme,
	id ComponentID,
	role FrameRole,
	title string,
	body Component,
	eventTarget WindowEventTarget,
	closable bool,
	preferW, preferH int,
) *WindowFrameWidget {
	tokens := NewTokenSet(th)
	frame := NewWindowFrame(id, role, tokens)
	frame.SetTitle(title)
	frame.SetClosable(closable)
	frame.SetContent(body)
	component := NewComponentWidget(th, frame, preferW, preferH)
	component.host.Resize(preferW, preferH)
	component.host.Layout()
	return &WindowFrameWidget{
		ComponentWidget: component,
		frame:           frame,
		eventTarget:     eventTarget,
	}
}

// Frame returns the unified visual frame.
func (w *WindowFrameWidget) Frame() *WindowFrame {
	if w == nil {
		return nil
	}
	return w.frame
}

// Body returns the component mounted inside the frame.
func (w *WindowFrameWidget) Body() Component {
	if w == nil || w.frame == nil {
		return nil
	}
	return w.frame.Content()
}

// Dispatch routes a message through the frame Host and returns the full
// EventResult so callers can honor bubbled actions (window.close, terminal
// float/dock). HandleEvent keeps the WindowContent tea.Cmd contract.
func (w *WindowFrameWidget) Dispatch(msg tea.Msg) EventResult {
	if w == nil || w.ComponentWidget == nil || w.host == nil {
		return EventResult{}
	}
	result := w.host.Handle(msg)
	if result.Handled {
		return result
	}
	if w.eventTarget == nil || w.frame == nil {
		return result
	}
	cmd := w.eventTarget.HandleEvent(translateFrameEvent(msg, w.frame.ContentBounds()))
	if cmd != nil {
		result.Cmd = cmd
		result.Handled = true
	}
	return result
}

// HandleEvent routes native component events first, then delegates unhandled
// events to a legacy body using content-local coordinates.
func (w *WindowFrameWidget) HandleEvent(msg tea.Msg) tea.Cmd {
	return w.Dispatch(msg).Cmd
}

func translateFrameEvent(msg tea.Msg, content Rect) tea.Msg {
	switch m := msg.(type) {
	case tea.MouseClickMsg:
		return tea.MouseClickMsg{X: m.X - content.X, Y: m.Y - content.Y, Button: m.Button}
	case tea.MouseMotionMsg:
		return tea.MouseMotionMsg{X: m.X - content.X, Y: m.Y - content.Y, Button: m.Button}
	case tea.MouseReleaseMsg:
		return tea.MouseReleaseMsg{X: m.X - content.X, Y: m.Y - content.Y, Button: m.Button}
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg{X: m.X - content.X, Y: m.Y - content.Y, Button: m.Button}
	default:
		return msg
	}
}

// RegisterTree registers c and all Parent descendants on the host.
func (h *Host) RegisterTree(c Component) {
	if h == nil || c == nil {
		return
	}
	h.Register(c)
	if p, ok := c.(Parent); ok {
		for _, ch := range p.Children() {
			h.RegisterTree(ch)
		}
	}
}

var _ wm.WindowContent = (*ComponentWidget)(nil)
var _ wm.WindowContent = (*WindowFrameWidget)(nil)
