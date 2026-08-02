// Package shelleditor provides the native DocumentEditor shell surface.
// PieceTable and core document ops stay in tui/editor; this package owns
// Host layout/paint of visual chrome (gutter, diagnostics, search,
// ScrollbarControl) into a dedicated compositor surface.
package shelleditor

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/widget"
)

const (
	ScreenID ui.ComponentID = "shelleditor"
	AreaID   ui.ComponentID = "shelleditor/area"
)

// Screen paints an editor.Editor into a Host surface via the cell-buffer
// Render path (not ANSI View). Status/actions live in shellstatus.
type Screen struct {
	ui.Base
	th      *theme.Theme
	tokens  *ui.TokenSet
	ed      *editor.Editor
	padL    int
	padR    int
	width   int
	height  int
	focused bool

	paintSurf *surface.Surface
}

// New creates a DocumentEditor shell screen.
func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	return &Screen{
		Base:   ui.NewBase(ScreenID),
		th:     th,
		tokens: ui.NewTokenSet(th),
	}
}

// Bind attaches the live editor for the next layout/paint pass.
func (s *Screen) Bind(ed *editor.Editor) {
	s.ed = ed
	if ed != nil {
		ed.SetTokens(s.tokens)
		ed.SetStatusChrome(false)
	}
}

// SetPadding sets left/right content padding columns around the editor body.
func (s *Screen) SetPadding(padL, padR int) {
	s.padL = padL
	s.padR = padR
}

// SetFocused marks whether the editor should paint a focused cursor.
func (s *Screen) SetFocused(v bool) { s.focused = v }

// SetTokens updates runtime theme tokens.
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.ed != nil {
		s.ed.SetTokens(tokens)
	}
}

// SetTheme replaces the active theme.
func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.tokens = ui.NewTokenSet(th)
	if s.ed != nil {
		s.ed.SetTokens(s.tokens)
	}
}

func (s *Screen) Children() []ui.Component { return nil }

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.width = r.W
	s.height = r.H
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	b := s.Bounds()
	if b.Empty() || buf == nil || s.ed == nil || !s.ed.IsOpen() {
		return
	}
	innerW := b.W - s.padL - s.padR
	if innerW < 10 {
		innerW = b.W
		s.padL, s.padR = 0, 0
	}
	h := b.H
	if h < 4 {
		return
	}

	bg := s.tokens.Color(ui.ColorBg)
	if s.padL > 0 || s.padR > 0 {
		ui.FillRect(buf, ox, oy, b, cell.BlankCell(bg))
	}

	if s.paintSurf == nil || s.paintSurf.Width() != innerW || s.paintSurf.Height() != h {
		s.paintSurf = surface.NewSurface("shelleditor-body", innerW, h, cell.TransparentBlank())
	}
	s.paintSurf.Buffer().Clear()
	s.ed.SetTokens(s.tokens)
	s.ed.SetStatusChrome(false)
	s.ed.SetPadding(s.padL)
	wctx := &widget.RenderContext{Theme: s.th, Focused: s.focused}
	s.ed.Render(s.paintSurf, wctx)
	surface.BlitOpaque(buf, ox+b.X+s.padL, oy+b.Y, s.paintSurf.Buffer())
}

func (s *Screen) Handle(_ ui.EventContext, _ tea.Msg) ui.EventResult {
	// Editor input remains routed through viewmodel → Editor.HandleEvent
	// until Host owns keyboard/mouse for DocumentEditor.
	return ui.EventResult{}
}

// CancelPointerCapture ends an in-progress mouse selection drag (and
// scrollbar thumb drag) without copying text. Implements
// ui.PointerCaptureCanceller so Host.CancelPointerCapture clears editor
// drag state when capture is torn down.
func (s *Screen) CancelPointerCapture() {
	if s.ed != nil {
		s.ed.CancelPointerCapture()
	}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleTextInput, Label: "Editor"}
}

// PaintTestBuffer lays out and paints into a fresh buffer for tests.
func (s *Screen) PaintTestBuffer(w, h int) *surface.CellBuffer {
	buf := surface.NewCellBuffer(w, h)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: w, H: h})
	s.Paint(buf, 0, 0, ui.PaintContext{Focused: s.focused})
	return buf
}

// PlainRow extracts a plain-text row from a painted buffer for tests.
func PlainRow(buf *surface.CellBuffer, y, maxW int) string {
	if buf == nil {
		return ""
	}
	var b strings.Builder
	for x := 0; x < maxW && x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.Char != 0 {
			b.WriteRune(c.Char)
		} else if !c.Transparent && c.Width > 0 {
			b.WriteRune(' ')
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// ScrollbarColumn returns the X index of the scrollbar within a painted row.
func ScrollbarColumn(buf *surface.CellBuffer, y int) int {
	if buf == nil {
		return -1
	}
	for x := buf.Width() - 1; x >= 0; x-- {
		c := buf.Get(x, y)
		if c.Char == '░' || c.Char == '█' {
			return x
		}
	}
	return -1
}
