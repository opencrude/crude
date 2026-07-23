package rendering

import (
	"crude/tui/cell"
	"crude/tui/surface"
)

// RenderToSurface parses an ANSI string and writes the cells into a surface.
// This is the bridge between legacy View()-returning widgets and the new
// compositing engine's cell buffer model.
//
// Handles:
// - SGR (Select Graphic Rendition) sequences: \x1b[...m
// - Regular characters → cells
// - Wide characters → wide cells + continuation
// - Newlines → row advancement
//
// The parser maintains a current style state (fg, bg, attrs) that is updated
// by SGR sequences. All subsequent characters are rendered with the current style
// until a new SGR sequence changes it.
func RenderToSurface(surf *surface.Surface, ansiStr string) {
	if surf == nil {
		return
	}
	buf := surf.Buffer()
	// ANSI views describe a complete frame. Clear reused buffers first so
	// cells from a previous page cannot leak through when the next view is
	// shorter, transparent, or changes layout entirely.
	buf.Clear()
	curX, curY := 0, 0
	w, h := buf.Width(), buf.Height()

	// Current style state
	curFG := cell.NoColor
	curBG := cell.NoColor
	curAttr := cell.Attrs(0)

	i := 0
	for i < len(ansiStr) {
		// Check for escape sequence
		if ansiStr[i] == '\x1b' && i+1 < len(ansiStr) && ansiStr[i+1] == '[' {
			// Find the end of the sequence (terminated by a letter)
			j := i + 2
			for j < len(ansiStr) && (ansiStr[j] >= '0' && ansiStr[j] <= '9' || ansiStr[j] == ';' || ansiStr[j] == ':') {
				j++
			}
			if j < len(ansiStr) {
				// ansiStr[j] is the final character (e.g., 'm' for SGR, 'H' for CUP)
				switch ansiStr[j] {
				case 'm':
					// SGR sequence
					parseSGR(ansiStr[i+2:j], &curFG, &curBG, &curAttr)
				case 'H':
					// Cursor position (CUP) — for future use
				}
				i = j + 1
				continue
			}
		}

		// Check for newline
		if ansiStr[i] == '\n' {
			curY++
			curX = 0
			i++
			continue
		}

		// Check for carriage return
		if ansiStr[i] == '\r' {
			curX = 0
			i++
			continue
		}

		// Regular character — handle grapheme clusters properly
		cluster, dw := FirstGrapheme(ansiStr[i:])
		if len(cluster) == 0 {
			i++
			continue
		}
		i += len(cluster)

		if dw == 0 {
			continue
		}

		// Skip if out of bounds
		if curY >= h || curX >= w {
			continue
		}

		if dw == 2 {
			if curX+1 >= w {
				// Wide char would be split, skip
				curX++
				continue
			}
			// Store the full cluster text in Content so GenerateANSI emits
			// every rune (e.g. ♻️ = ♻ + FE0F, ⚡️ = ⚡ + FE0F). Without it,
			// ultraviolet re-parses a truncated glyph and computes the
			// wrong width — every later column shifts, producing the ghost
			// artifacts reported on Windows.
			buf.Set(curX, curY, cell.NewClusterCell(cluster, 2, curFG, curBG, curAttr))
			buf.Set(curX+1, curY, cell.Cell{Char: 0, Width: 0, FG: curFG, BG: curBG, Attr: curAttr})
			curX += 2
		} else {
			buf.Set(curX, curY, cell.NewClusterCell(cluster, 1, curFG, curBG, curAttr))
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
		case p.hasSub && p.base == 4:
			switch p.sub {
			case 0:
				*attr &^= cell.AttrUnderline | cell.AttrUndercurl | cell.AttrUnderlineDotted
			case 1:
				*attr &^= cell.AttrUndercurl | cell.AttrUnderlineDotted
				*attr |= cell.AttrUnderline
			case 2:
				*attr &^= cell.AttrUndercurl | cell.AttrUnderlineDotted
				*attr |= cell.AttrUnderline
			case 3:
				*attr &^= cell.AttrUnderlineDotted
				*attr |= cell.AttrUnderline | cell.AttrUndercurl
			case 4:
				*attr &^= cell.AttrUndercurl
				*attr |= cell.AttrUnderline | cell.AttrUnderlineDotted
			}
		case p.base == 0:
			*fg = cell.NoColor
			*bg = cell.NoColor
			*attr = 0
		case p.base == 1:
			*attr |= cell.AttrBold
		case p.base == 2:
			*attr |= cell.AttrDim
		case p.base == 3:
			*attr |= cell.AttrItalic
		case p.base == 4:
			*attr |= cell.AttrUnderline
		case p.base == 5:
			*attr |= cell.AttrBlink
		case p.base == 7:
			*attr |= cell.AttrReverse
		case p.base == 9:
			*attr |= cell.AttrStrikethrough
		case p.base == 22:
			*attr &^= cell.AttrBold | cell.AttrDim
		case p.base == 23:
			*attr &^= cell.AttrItalic
		case p.base == 24:
			*attr &^= cell.AttrUnderline | cell.AttrUndercurl | cell.AttrUnderlineDotted
		case p.base == 25:
			*attr &^= cell.AttrBlink
		case p.base == 27:
			*attr &^= cell.AttrReverse
		case p.base == 29:
			*attr &^= cell.AttrStrikethrough
		case p.base == 39:
			*fg = cell.NoColor
		case p.base == 49:
			*bg = cell.NoColor
		case p.base >= 30 && p.base <= 37:
			*fg = cell.NewPaletteColor(uint8(p.base - 30))
		case p.base >= 40 && p.base <= 47:
			*bg = cell.NewPaletteColor(uint8(p.base - 40))
		case p.base >= 90 && p.base <= 97:
			*fg = cell.NewPaletteColor(uint8(p.base - 90 + 8))
		case p.base >= 100 && p.base <= 107:
			*bg = cell.NewPaletteColor(uint8(p.base - 100 + 8))
		case p.base == 38:
			c, consumed := parseExtendedColor(parts[i+1:])
			*fg = c
			i += consumed
		case p.base == 48:
			c, consumed := parseExtendedColor(parts[i+1:])
			*bg = c
			i += consumed
		}
		i++
	}
}

type sgrParam struct {
	base   int
	sub    int
	hasSub bool
}

// splitParams splits an SGR parameter string by semicolons.
// It preserves colon subparameters (e.g. 4:3 for wavy underline) in sgrParam.
func splitParams(s string) []sgrParam {
	count := 1
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			count++
		}
	}
	result := make([]sgrParam, 0, count)
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ';' {
			if i > start {
				result = append(result, parseSgrParam(s[start:i]))
			} else {
				result = append(result, sgrParam{base: 0})
			}
			start = i + 1
		}
	}
	return result
}

func parseSgrParam(s string) sgrParam {
	base := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			sub := 0
			for j := i + 1; j < len(s); j++ {
				if s[j] >= '0' && s[j] <= '9' {
					sub = sub*10 + int(s[j]-'0')
				}
			}
			return sgrParam{base: base, sub: sub, hasSub: true}
		}
		if s[i] >= '0' && s[i] <= '9' {
			base = base*10 + int(s[i]-'0')
		}
	}
	return sgrParam{base: base}
}

// parseExtendedColor parses a 256-color or truecolor sequence.
// Returns the color and the number of additional parameters consumed.
func parseExtendedColor(params []sgrParam) (cell.Color, int) {
	if len(params) == 0 {
		return cell.NoColor, 0
	}
	switch params[0].base {
	case 5:
		if len(params) >= 2 {
			return cell.NewPaletteColor(uint8(params[1].base)), 2
		}
		return cell.NoColor, 1
	case 2:
		if len(params) >= 4 {
			return cell.NewTrueColor(uint8(params[1].base), uint8(params[2].base), uint8(params[3].base)), 4
		}
		return cell.NoColor, 1
	default:
		return cell.NoColor, 0
	}
}
