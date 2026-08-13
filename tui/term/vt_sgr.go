package term

import (
	"github.com/charmbracelet/x/ansi"

	"crude/tui/cell"
)

func (e *Emulator) handleSGR(params ansi.Params) {
	b := e.buf()
	if len(params) == 0 {
		b.pen = pen{}
		return
	}
	i := 0
	for i < len(params) {
		p, _, _ := params.Param(i, 0)
		switch p {
		case 0:
			b.pen = pen{}
		case 1:
			b.pen.attr |= cell.AttrBold
		case 2:
			b.pen.attr |= cell.AttrDim
		case 3:
			b.pen.attr |= cell.AttrItalic
		case 4:
			b.pen.attr |= cell.AttrUnderline
		case 5, 6:
			b.pen.attr |= cell.AttrBlink
		case 7:
			b.pen.attr |= cell.AttrReverse
		case 9:
			b.pen.attr |= cell.AttrStrikethrough
		case 22:
			b.pen.attr &^= cell.AttrBold | cell.AttrDim
		case 23:
			b.pen.attr &^= cell.AttrItalic
		case 24:
			b.pen.attr &^= cell.AttrUnderline | cell.AttrUndercurl | cell.AttrUnderlineDotted
		case 25:
			b.pen.attr &^= cell.AttrBlink
		case 27:
			b.pen.attr &^= cell.AttrReverse
		case 29:
			b.pen.attr &^= cell.AttrStrikethrough
		case 39:
			b.pen.fg = cell.NoColor
		case 49:
			b.pen.bg = cell.NoColor
		case 38, 48:
			color, consumed := sgrColor(params, i)
			if p == 38 {
				b.pen.fg = color
			} else {
				b.pen.bg = color
			}
			i += consumed
			continue
		default:
			if p >= 30 && p <= 37 {
				b.pen.fg = cell.Color{Type: cell.ColorPalette, Palette: uint8(p - 30)}
			} else if p >= 40 && p <= 47 {
				b.pen.bg = cell.Color{Type: cell.ColorPalette, Palette: uint8(p - 40)}
			} else if p >= 90 && p <= 97 {
				b.pen.fg = cell.Color{Type: cell.ColorPalette, Palette: uint8(p - 90 + 8)}
			} else if p >= 100 && p <= 107 {
				b.pen.bg = cell.Color{Type: cell.ColorPalette, Palette: uint8(p - 100 + 8)}
			}
		}
		i++
	}
}

func sgrColor(params ansi.Params, i int) (cell.Color, int) {
	// CSI 38 ; 5 ; n  or  38 ; 2 ; r ; g ; b
	if i+1 >= len(params) {
		return cell.NoColor, 1
	}
	mode, _, _ := params.Param(i+1, 0)
	switch mode {
	case 5:
		if i+2 >= len(params) {
			return cell.NoColor, 2
		}
		idx, _, _ := params.Param(i+2, 0)
		return cell.Color{Type: cell.ColorPalette, Palette: uint8(idx)}, 3
	case 2:
		if i+4 >= len(params) {
			return cell.NoColor, 2
		}
		r, _, _ := params.Param(i+2, 0)
		g, _, _ := params.Param(i+3, 0)
		bl, _, _ := params.Param(i+4, 0)
		c := cell.Color{Type: cell.ColorTrueColor}
		c.R, c.G, c.B = uint8(r), uint8(g), uint8(bl)
		return c, 5
	}
	return cell.NoColor, 2
}
