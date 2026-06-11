package rendering

import (
	"crude/tui/cell"
	"crude/tui/surface"

	"github.com/rivo/uniseg"
)

// RenderToSurface parses an ANSI string and writes the cells into a surface.
// This is the bridge between legacy View()-returning widgets and the new
// compositing engine's cell buffer model.
//
// Handles:
//   - SGR (Select Graphic Rendition) sequences: \x1b[...m
//   - Regular characters → cells
//   - Wide characters → wide cells + continuation
//   - Newlines → row advancement
//
// The parser maintains a current style state (fg, bg, attrs) that is updated
// by SGR sequences. All subsequent characters are rendered with the current style
// until a new SGR sequence changes it.
func RenderToSurface(surf *surface.Surface, ansi string) {
	buf := surf.Buffer()
	curX, curY := 0, 0
	w, h := buf.Width(), buf.Height()

	// Current style state
	curFG := cell.NoColor
	curBG := cell.NoColor
	curAttr := cell.Attrs(0)

	i := 0
	for i < len(ansi) {
		// Check for escape sequence
		if ansi[i] == '\x1b' && i+1 < len(ansi) && ansi[i+1] == '[' {
			// Find the end of the sequence (terminated by a letter)
			j := i + 2
			for j < len(ansi) && (ansi[j] >= '0' && ansi[j] <= '9' || ansi[j] == ';') {
				j++
			}
			if j < len(ansi) {
				// ansi[j] is the final character (e.g., 'm' for SGR, 'H' for CUP)
				switch ansi[j] {
				case 'm':
					// SGR sequence
					parseSGR(ansi[i+2:j], &curFG, &curBG, &curAttr)
				case 'H':
					// Cursor position (CUP) — for future use
				}
				i = j + 1
				continue
			}
		}

		// Check for newline
		if ansi[i] == '\n' {
			curY++
			curX = 0
			i++
			continue
		}

		// Check for carriage return
		if ansi[i] == '\r' {
			curX = 0
			i++
			continue
		}

		// Regular character — handle grapheme clusters properly
		clusterStart := i
		gr := uniseg.NewGraphemes(ansi[i:])
		gr.Next()
		cluster := gr.Str()
		i = clusterStart + len(cluster)

		// Skip zero-width clusters (combining characters, etc.)
		displayWidth := uniseg.StringWidth(cluster)
		if displayWidth == 0 {
			continue
		}

		// Skip if out of bounds
		if curY >= h || curX >= w {
			continue
		}

		r := []rune(cluster)[0]
		if displayWidth == 2 {
			if curX+1 >= w {
				// Wide char would be split, skip
				curX++
				continue
			}
			buf.Set(curX, curY, cell.Cell{Char: r, Width: 2, FG: curFG, BG: curBG, Attr: curAttr})
			buf.Set(curX+1, curY, cell.Cell{Char: 0, Width: 0, FG: curFG, BG: curBG, Attr: curAttr})
			curX += 2
		} else {
			buf.Set(curX, curY, cell.Cell{Char: r, Width: 1, FG: curFG, BG: curBG, Attr: curAttr})
			curX++
		}
	}
}

// parseSGR parses an SGR parameter string and updates the style state.
// The params string is the content between \x1b[ and m (e.g., "0;1;38;2;255;100;50")
func parseSGR(params string, fg, bg *cell.Color, attr *cell.Attrs) {
	if params == "" {
		// \x1b[m is equivalent to \x1b[0m (reset)
		*fg = cell.NoColor
		*bg = cell.NoColor
		*attr = 0
		return
	}

	parts := splitParams(params)

	i := 0
	for i < len(parts) {
		p := parts[i]
		switch {
		case p == 0:
			*fg = cell.NoColor
			*bg = cell.NoColor
			*attr = 0
		case p == 1:
			*attr |= cell.AttrBold
		case p == 2:
			*attr |= cell.AttrDim
		case p == 3:
			*attr |= cell.AttrItalic
		case p == 4:
			*attr |= cell.AttrUnderline
		case p == 5:
			*attr |= cell.AttrBlink
		case p == 7:
			*attr |= cell.AttrReverse
		case p == 9:
			*attr |= cell.AttrStrikethrough
		case p == 22:
			*attr &^= cell.AttrBold | cell.AttrDim
		case p == 23:
			*attr &^= cell.AttrItalic
		case p == 24:
			*attr &^= cell.AttrUnderline
		case p == 25:
			*attr &^= cell.AttrBlink
		case p == 27:
			*attr &^= cell.AttrReverse
		case p == 29:
			*attr &^= cell.AttrStrikethrough
		case p == 39:
			*fg = cell.NoColor
		case p == 49:
			*bg = cell.NoColor
		case p >= 30 && p <= 37:
			*fg = cell.NewPaletteColor(uint8(p - 30))
		case p >= 40 && p <= 47:
			*bg = cell.NewPaletteColor(uint8(p - 40))
		case p >= 90 && p <= 97:
			*fg = cell.NewPaletteColor(uint8(p - 90 + 8))
		case p >= 100 && p <= 107:
			*bg = cell.NewPaletteColor(uint8(p - 100 + 8))
		case p == 38:
			c, consumed := parseExtendedColor(parts[i+1:])
			*fg = c
			i += consumed
		case p == 48:
			c, consumed := parseExtendedColor(parts[i+1:])
			*bg = c
			i += consumed
		}
		i++
	}
}

// splitParams splits an SGR parameter string by semicolons.
// Returns a slice of integers. Empty segments are treated as 0.
func splitParams(s string) []int {
	count := 1
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			count++
		}
	}
	result := make([]int, 0, count)
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ';' {
			if i > start {
				n := 0
				for j := start; j < i; j++ {
					if s[j] >= '0' && s[j] <= '9' {
						n = n*10 + int(s[j]-'0')
					}
				}
				result = append(result, n)
			} else {
				result = append(result, 0)
			}
			start = i + 1
		}
	}
	return result
}

// parseExtendedColor parses a 256-color or truecolor sequence.
// Returns the color and the number of additional parameters consumed.
func parseExtendedColor(params []int) (cell.Color, int) {
	if len(params) == 0 {
		return cell.NoColor, 0
	}
	switch params[0] {
	case 5:
		if len(params) >= 2 {
			return cell.NewPaletteColor(uint8(params[1])), 2
		}
		return cell.NoColor, 1
	case 2:
		if len(params) >= 4 {
			return cell.NewTrueColor(uint8(params[1]), uint8(params[2]), uint8(params[3])), 4
		}
		return cell.NoColor, 1
	default:
		return cell.NoColor, 0
	}
}
