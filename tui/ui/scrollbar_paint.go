package ui

import (
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

// ScrollbarCell returns the track or thumb cell for one row of a vertical track.
func ScrollbarCell(scroll *ScrollModel, trackH, trackRow int, tokens *TokenSet, bg cell.Color) cell.Cell {
	if scroll == nil || trackH <= 0 || trackRow < 0 || trackRow >= trackH {
		return cell.BlankCell(bg)
	}
	if tokens == nil {
		tokens = NewTokenSet(theme.DefaultDark())
	}
	if bg.IsDefault() {
		bg = tokens.Color(ColorBg)
	}
	trackFG := tokens.Color(ColorBorder)
	thumbFG := tokens.Color(ColorMuted)
	if scroll.MaxOffset() <= 0 {
		return cell.NewCell('░', trackFG, bg, 0)
	}
	top, th := scroll.ThumbBounds(trackH)
	ch := rune('░')
	fg := trackFG
	if trackRow >= top && trackRow < top+th {
		ch = '█'
		fg = thumbFG
	}
	return cell.NewCell(ch, fg, bg, 0)
}

// PaintScrollbarCell writes one track/thumb cell at (x,y).
func PaintScrollbarCell(buf *surface.CellBuffer, x, y int, scroll *ScrollModel, trackH, trackRow int, tokens *TokenSet, bg cell.Color) {
	if buf == nil {
		return
	}
	buf.Set(x, y, ScrollbarCell(scroll, trackH, trackRow, tokens, bg))
}
