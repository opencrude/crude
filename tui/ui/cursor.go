package ui

import (
	"crude/tui/cell"
	"crude/tui/surface"
)

// PaintBlockCursor overlays a reverse-video block on the cell already at x,y.
// It preserves the glyph, display width, and layout position. At an empty
// insertion point it reverses a blank cell using the supplied base colors.
func PaintBlockCursor(buf *surface.CellBuffer, x, y int, baseFG, baseBG cell.Color) {
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
	if c.FG.IsDefault() {
		c.FG = baseFG
	}
	if c.BG.IsDefault() {
		c.BG = baseBG
	}
	c.Transparent = false
	c.Attr ^= cell.AttrReverse
	buf.Set(x, y, c)
}
