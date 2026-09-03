package ui

import (
	"crude/tui/cell"
	"crude/tui/surface"
)

const (
	CursorActivityHoldMS    = 800
	CursorBlinkHalfPeriodMS = 400
)

type CursorBlinkState struct {
	anchor      int
	initialized bool
	pending     bool
}

func (s *CursorBlinkState) MarkActivity() { s.pending = true }
func (s *CursorBlinkState) ResetAt(frame int) {
	s.anchor = frame
	s.initialized = true
	s.pending = false
}
func (s *CursorBlinkState) Visible(frame int) bool {
	if !s.initialized || s.pending || frame < s.anchor {
		s.ResetAt(frame)
		return true
	}
	return CursorPhaseVisible(frame, s.anchor, true)
}

func CursorPhaseVisible(frame, activityFrame int, blinking bool) bool {
	if !blinking {
		return true
	}
	if frame-activityFrame < CursorActivityHoldMS {
		return true
	}
	return (frame/CursorBlinkHalfPeriodMS)%2 == 0
}

// PaintBlockCursor overlays a reverse-video block on the cell already at x,y.
// It preserves the glyph, display width, and layout position. At an empty
// insertion point it reverses a blank cell using the supplied base colors.
func PaintBlockCursor(buf *surface.CellBuffer, x, y int, cursorFG, baseBG cell.Color) {
	if buf == nil || x < 0 || y < 0 || x >= buf.Width() || y >= buf.Height() {
		return
	}
	c := buf.Get(x, y)
	if c.Width == 0 {
		return
	}
	if c.Char == 0 {
		c.Char = ' '
		c.Width = 1
	}
	if !cursorFG.IsDefault() {
		c.FG = cursorFG
	}
	if c.BG.IsDefault() {
		c.BG = baseBG
	}
	c.Transparent = false
	c.Attr ^= cell.AttrReverse
	buf.Set(x, y, c)
}
