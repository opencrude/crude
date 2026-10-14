// Package shellinput provides the native chat-input shell surface.
package shellinput

import (
	"fmt"
	"strings"

	"crude/tui/cell"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID ui.ComponentID = "shellinput"
	AreaID   ui.ComponentID = "shellinput/area"
	SlashID  ui.ComponentID = "shellinput/slash"
	ChipsID  ui.ComponentID = "shellinput/chips"
)

// Screen paints the chat composer chrome: separator, reference chips,
// TextArea, and slash-command List into a Host surface.
type Screen struct {
	ui.Base
	th     *theme.Theme
	tokens *ui.TokenSet
	model  *input.Model
	width  int
	height int
}

// New creates a chat-input shell screen.
func New(th *theme.Theme) *Screen {
	return NewWithID(th, ScreenID)
}

// NewWithID mounts a session-owned composer without sharing hit-test identity.
func NewWithID(th *theme.Theme, id ui.ComponentID) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	return &Screen{
		Base:   ui.NewBase(id),
		th:     th,
		tokens: ui.NewTokenSet(th),
	}
}

// Bind attaches the live input model for the next layout/paint pass.
func (s *Screen) Bind(m *input.Model) { s.model = m }

// SetTokens updates runtime theme tokens.
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.model != nil {
		s.model.SetTokens(tokens)
	}
}

// SetTheme replaces the active theme.
func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.tokens = ui.NewTokenSet(th)
	if s.model != nil {
		s.model.SetTheme(th)
		s.model.SetTokens(s.tokens)
	}
}

func (s *Screen) SetWidth(w int) { s.width = w }

// ContentHeight returns the number of rows the shell consumes for width.
func (s *Screen) ContentHeight(width int) int {
	if s.model == nil {
		return 1
	}
	return s.model.ShellHeight(width)
}

func (s *Screen) Children() []ui.Component { return nil }

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	h := 1
	if s.model != nil {
		h = s.model.ShellHeight(cs.MaxW)
	}
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: h})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.width = r.W
	s.height = r.H
	if s.model != nil {
		s.model.LayoutShell(r.W, r.H)
	}
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	b := s.Bounds()
	if b.Empty() || buf == nil || s.model == nil {
		return
	}
	bg := s.tokens.Color(ui.ColorBg)
	ui.FillRect(buf, ox, oy, b, cell.BlankCell(bg))
	s.model.PaintShell(buf, ox+b.X, oy+b.Y, b.W, b.H, ctx)
	if first, last, total := s.model.ComposerViewport(); first > 0 && last-first+1 < total {
		hint := fmt.Sprintf("↑↓ %d–%d/%d", first, last, total)
		pad := min(s.tokens.PadLR(), b.W/2)
		available := b.W - 2*pad
		if rendering.VisibleWidth(hint) <= available {
			buf.WriteString(ox+b.X+pad, oy+b.Y, hint, s.tokens.Color(ui.ColorMuted), bg, 0)
		}
	}
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.model == nil {
		return ui.EventResult{}
	}
	return s.model.HandleShell(ctx, evt)
}

// CancelPointerCapture resets the composer's drag/selection state when the Host
// revokes pointer capture (another window claimed the pointer, slot hidden).
// Without it the TextArea's drag flag outlived the capture and the viewmodel
// kept routing pointer events into the composer.
func (s *Screen) CancelPointerCapture() {
	if s.model != nil {
		s.model.CancelPointerCapture()
	}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleTextInput, Label: "Chat input"}
}

func (s *Screen) PaintTestBuffer(w, h int) *surface.CellBuffer {
	buf := surface.NewCellBuffer(w, h)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: w, H: h})
	s.Paint(buf, 0, 0, ui.PaintContext{Focused: true})
	return buf
}

func PlainRow(buf *surface.CellBuffer, y, maxW int) string {
	if buf == nil {
		return ""
	}
	var b strings.Builder
	for x := 0; x < maxW && x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.Char != 0 {
			b.WriteRune(c.Char)
		}
	}
	return strings.TrimRight(b.String(), " ")
}
