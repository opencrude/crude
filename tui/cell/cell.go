// Package cell defines the primitive types for the compositing engine.
//
// Layer 0 of the compositing architecture. All higher layers (surface,
// compositor, widget, wm) depend on the types defined here.
//
// Core types:
// - Color: terminal color (default, 256-palette, 24-bit truecolor)
// - Attrs: text rendering attributes as a bitmask (bold, italic, etc.)
// - Cell: the atomic unit of compositing — a single terminal cell
//
// Pool types (pool.go):
// - CharPool: interns frequently-used runes to reduce memory
// - StylePool: interns color+attribute combinations to reduce per-cell overhead
package cell

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Color
// ---------------------------------------------------------------------------

// ColorType identifies how a color is represented in the terminal.
type ColorType uint8

const (
	// ColorDefault means the terminal's default foreground/background color.
	// No ANSI escape sequence is emitted for default colors.
	ColorDefault ColorType = iota

	// ColorPalette means a 256-color palette index (0-255).
	// Emits \x1b[38;5;Nm (foreground) or \x1b[48;5;Nm (background).
	ColorPalette

	// ColorTrueColor means a 24-bit RGB color.
	// Emits \x1b[38;2;R;G;Bm (foreground) or \x1b[48;2;R;G;Bm (background).
	ColorTrueColor
)

// Color represents a terminal color. Supports default, palette, and truecolor.
// Color is a value type — it is always passed by copy, never by pointer.
type Color struct {
	Type    ColorType
	Palette uint8 // For ColorPalette: index 0-255
	R, G, B uint8 // For ColorTrueColor: RGB components
}

// Predefined colors for convenience. Palette indices follow the standard
// xterm 16-color map (indices 0-15).
var (
	NoColor       = Color{Type: ColorDefault}
	Black         = Color{Type: ColorPalette, Palette: 0}
	Red           = Color{Type: ColorPalette, Palette: 1}
	Green         = Color{Type: ColorPalette, Palette: 2}
	Yellow        = Color{Type: ColorPalette, Palette: 3}
	Blue          = Color{Type: ColorPalette, Palette: 4}
	Magenta       = Color{Type: ColorPalette, Palette: 5}
	Cyan          = Color{Type: ColorPalette, Palette: 6}
	White         = Color{Type: ColorPalette, Palette: 7}
	BrightBlack   = Color{Type: ColorPalette, Palette: 8} // Dark gray
	BrightRed     = Color{Type: ColorPalette, Palette: 9}
	BrightGreen   = Color{Type: ColorPalette, Palette: 10}
	BrightYellow  = Color{Type: ColorPalette, Palette: 11}
	BrightBlue    = Color{Type: ColorPalette, Palette: 12}
	BrightMagenta = Color{Type: ColorPalette, Palette: 13}
	BrightCyan    = Color{Type: ColorPalette, Palette: 14}
	BrightWhite   = Color{Type: ColorPalette, Palette: 15} // Light gray

	// Gray ramps using palette indices 232-255 (24-step grayscale).
	GrayDark  = Color{Type: ColorPalette, Palette: 232}
	GrayMid   = Color{Type: ColorPalette, Palette: 244}
	GrayLight = Color{Type: ColorPalette, Palette: 253}
)

// NewTrueColor creates a 24-bit RGB truecolor.
func NewTrueColor(r, g, b uint8) Color {
	return Color{Type: ColorTrueColor, R: r, G: g, B: b}
}

// NewPaletteColor creates a 256-color palette entry.
func NewPaletteColor(idx uint8) Color {
	return Color{Type: ColorPalette, Palette: idx}
}

// ANSIFG returns the ANSI escape sequence fragment for setting this color
// as the foreground. Returns an empty string for default colors.
func (c Color) ANSIFG() string {
	switch c.Type {
	case ColorDefault:
		return ""
	case ColorPalette:
		return fmt.Sprintf("\x1b[38;5;%dm", c.Palette)
	case ColorTrueColor:
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.R, c.G, c.B)
	default:
		return ""
	}
}

// ANSIBG returns the ANSI escape sequence fragment for setting this color
// as the background. Returns an empty string for default colors.
func (c Color) ANSIBG() string {
	switch c.Type {
	case ColorDefault:
		return ""
	case ColorPalette:
		return fmt.Sprintf("\x1b[48;5;%dm", c.Palette)
	case ColorTrueColor:
		return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.R, c.G, c.B)
	default:
		return ""
	}
}

// Equal reports whether two colors are identical.
func (c Color) Equal(other Color) bool {
	if c.Type != other.Type {
		return false
	}
	switch c.Type {
	case ColorDefault:
		return true
	case ColorPalette:
		return c.Palette == other.Palette
	case ColorTrueColor:
		return c.R == other.R && c.G == other.G && c.B == other.B
	default:
		return false
	}
}

// IsDefault reports whether this is the terminal default color.
func (c Color) IsDefault() bool {
	return c.Type == ColorDefault
}

// Blend performs Porter-Duff alpha compositing of this color over another.
//
// The semantics are: result = over * alpha + this * (1-alpha).
// - If over.IsDefault(), return this (the bottom color is unchanged).
// - If alpha >= 1.0, return over (fully opaque overlay).
// - If the bottom (this) color is default, treat it as black (0,0,0) for blending.
//
// Blend always returns a TrueColor result when actual blending occurs,
// since palette indices cannot represent blended values.
func (c Color) Blend(over Color, alpha float32) Color {
	// If the overlay is default, the bottom color shows through unchanged.
	if over.IsDefault() {
		return c
	}

	// Guard against NaN (NaN != NaN) and alpha <= 0 (no blending).
	if alpha != alpha || alpha <= 0 {
		return c
	}

	// If alpha >= 1.0, the overlay fully replaces the bottom.
	if alpha >= 1.0 {
		return over
	}

	// Convert the overlay to RGB for blending.
	oR, oG, oB := colorToRGB(over)

	// Convert the bottom (this) to RGB. Default → black (0,0,0).
	var bR, bG, bB uint8
	if !c.IsDefault() {
		bR, bG, bB = colorToRGB(c)
	}

	// Porter-Duff: result = over * alpha + this * (1 - alpha)
	r := uint8(float32(oR)*alpha + float32(bR)*(1-alpha))
	g := uint8(float32(oG)*alpha + float32(bG)*(1-alpha))
	b := uint8(float32(oB)*alpha + float32(bB)*(1-alpha))

	return NewTrueColor(r, g, b)
}

// ContrastColor returns a readable foreground for bg: near-black on light
// fills, near-white on dark fills. Buttons use this so variant-colored hover
// chips stay legible on every theme.
func ContrastColor(bg Color) Color {
	r, g, b := colorToRGB(bg)
	luma := 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)
	if luma > 140 {
		return NewTrueColor(0x0d, 0x11, 0x17)
	}
	return NewTrueColor(0xf0, 0xf6, 0xfc)
}

// colorToRGB extracts RGB components from a Color.
// For palette colors, returns the palette index as a gray approximation.
// This is a helper for Blend; in practice, the compositor may want to
// resolve palette colors to actual RGB values using a palette map.
func colorToRGB(c Color) (r, g, b uint8) {
	switch c.Type {
	case ColorTrueColor:
		return c.R, c.G, c.B
	case ColorPalette:
		// Approximate palette colors to RGB for blending.
		// Standard 16 colors:
		switch c.Palette {
		case 0: // Black
			return 0, 0, 0
		case 1: // Red
			return 205, 0, 0
		case 2: // Green
			return 0, 205, 0
		case 3: // Yellow
			return 205, 205, 0
		case 4: // Blue
			return 0, 0, 205
		case 5: // Magenta
			return 205, 0, 205
		case 6: // Cyan
			return 0, 205, 205
		case 7: // White (light gray)
			return 229, 229, 229
		case 8: // Bright Black (dark gray)
			return 127, 127, 127
		case 9: // Bright Red
			return 255, 0, 0
		case 10: // Bright Green
			return 0, 255, 0
		case 11: // Bright Yellow
			return 255, 255, 0
		case 12: // Bright Blue
			return 0, 0, 255
		case 13: // Bright Magenta
			return 255, 0, 255
		case 14: // Bright Cyan
			return 0, 255, 255
		case 15: // Bright White
			return 255, 255, 255
		default:
			// Palette indices 16-231: 6x6x6 color cube
			// Palette indices 232-255: 24-step grayscale ramp
			if c.Palette >= 232 {
				v := uint8(8 + 10*(int(c.Palette)-232))
				return v, v, v
			}
			// 6x6x6 color cube: indices 16-231
			idx := int(c.Palette) - 16
			ri := (idx / 36) * 40
			gi := ((idx % 36) / 6) * 40
			bi := (idx % 6) * 40
			if ri > 0 {
				ri += 55
			}
			if gi > 0 {
				gi += 55
			}
			if bi > 0 {
				bi += 55
			}
			return uint8(ri), uint8(gi), uint8(bi)
		}
	default:
		return 0, 0, 0
	}
}

// ---------------------------------------------------------------------------
// Attributes
// ---------------------------------------------------------------------------

// Attrs represents text rendering attributes as a bitmask.
// Multiple attributes can be combined with bitwise OR.
type Attrs uint16

const (
	AttrBold            Attrs = 1 << iota // Bold intensity
	AttrItalic                            // Italic text
	AttrUnderline                         // Underlined text
	AttrStrikethrough                     // Strikethrough text
	AttrReverse                           // Swap foreground/background
	AttrBlink                             // Blinking text
	AttrDim                               // Dim/faint intensity
	AttrUndercurl                         // Wavy/curly underline (supported by kitty, WezTerm, foot, Windows Terminal)
	AttrUnderlineDotted                   // Dotted underline (supported by kitty, WezTerm, foot, Windows Terminal)
)

// ANSI returns the ANSI escape sequence for enabling these attributes.
// The sequence starts with a reset (\x1b[0m) and then sets each active bit.
// This ensures no stale attributes leak from previous cells.
//
// For underline styles (wavy, dotted), we use the modern ECMA-48 / kitty
// extended sequences: \x1b[4:3m (wavy) and \x1b[4:4m (dotted). These are
// widely supported by kitty, WezTerm, foot, and Windows Terminal ≥ 1.22.
// When AttrUnderline is combined with AttrUndercurl or AttrUnderlineDotted,
// the more specific style takes precedence.
func (a Attrs) ANSI() string {
	if a == 0 {
		return "\x1b[0m"
	}

	var sb strings.Builder
	sb.WriteString("\x1b[0")
	if a&AttrBold != 0 {
		sb.WriteString(";1")
	}
	if a&AttrDim != 0 {
		sb.WriteString(";2")
	}
	if a&AttrItalic != 0 {
		sb.WriteString(";3")
	}
	if a&AttrUnderline != 0 || a&AttrUndercurl != 0 || a&AttrUnderlineDotted != 0 {
		sb.WriteString(";4")
	}
	if a&AttrBlink != 0 {
		sb.WriteString(";5")
	}
	if a&AttrReverse != 0 {
		sb.WriteString(";7")
	}
	if a&AttrStrikethrough != 0 {
		sb.WriteString(";9")
	}
	if a&AttrUndercurl != 0 {
		sb.WriteString(";4:3")
	}
	if a&AttrUnderlineDotted != 0 {
		sb.WriteString(";4:4")
	}
	sb.WriteString("m")
	return sb.String()
}

// Has reports whether all bits in the given mask are set.
func (a Attrs) Has(mask Attrs) bool {
	return a&mask == mask
}

// ---------------------------------------------------------------------------
// Cell
// ---------------------------------------------------------------------------

// Cell represents a single terminal cell — the atomic unit of compositing.
//
// Each cell occupies one or more columns on the terminal grid.
// A wide character (CJK, emoji) occupies 2 columns and produces:
// - Cell{Char: '界', Width: 2} at column N
// - Cell{Char: 0, Width: 0} at column N+1 (continuation cell)
//
// A Transparent cell does not overwrite the cell below during compositing.
// This is how "holes" in floating windows let background content show through.
type Cell struct {
	Char        rune   // The character to display (0 for continuation cells)
	Content     string // Full grapheme cluster text (Char is just the first rune)
	Width       uint8  // Display width: 0 (continuation), 1 (normal), 2 (wide)
	FG          Color  // Foreground color
	BG          Color  // Background color
	Attr        Attrs  // Text rendering attributes
	Transparent bool   // If true, compositing skips this cell (shows below)
}

// NewCell creates a normal-width cell with the given character and style.
func NewCell(ch rune, fg, bg Color, attr Attrs) Cell {
	return Cell{Char: ch, Content: string(ch), Width: 1, FG: fg, BG: bg, Attr: attr}
}

// NewWideCell creates a wide (2-column) cell.
// The caller is responsible for placing a ContinuationCell at the next column.
func NewWideCell(ch rune, fg, bg Color, attr Attrs) Cell {
	return Cell{Char: ch, Content: string(ch), Width: 2, FG: fg, BG: bg, Attr: attr}
}

// NewClusterCell creates a cell whose visible glyph is a full grapheme
// cluster (e.g. ♻️ = ♻ + FE0F, 👨‍👩‍👧‍👦, 🇯🇵). Storing the cluster text in
// Content keeps the ANSI lossy step lossless — GenerateANSI emits every
// rune, so ultraviolet re-parses the same cluster and computes the same
// width. width is the terminal advance for the whole cluster (0, 1 or 2).
func NewClusterCell(cluster string, width int, fg, bg Color, attr Attrs) Cell {
	r := firstRune(cluster)
	return Cell{Char: r, Content: cluster, Width: uint8(width), FG: fg, BG: bg, Attr: attr}
}

// firstRune returns the first rune of s, or 0 if empty.
func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

// ContinuationCell is the placeholder for the second column of a wide character.
// It has Char=0 and Width=0, signaling that the previous cell spans this column.
var ContinuationCell = Cell{Char: 0, Width: 0}

// BlankCell creates a space cell with the given background.
// The foreground is default (no color) and no attributes are set.
func BlankCell(bg Color) Cell {
	return Cell{Char: ' ', Width: 1, BG: bg}
}

// TransparentBlank creates a transparent space cell.
// During compositing, this cell is skipped, allowing content below to show through.
func TransparentBlank() Cell {
	return Cell{Char: ' ', Width: 1, Transparent: true}
}

// IsBlank reports whether the cell is an empty space (no visible character).
// Both space characters and continuation cells (Char=0) are considered blank.
func (c Cell) IsBlank() bool {
	return c.Char == ' ' || c.Char == 0
}

// IsVisible reports whether the cell has any visible content.
// A cell is visible if it is not transparent and contains a non-blank character.
func (c Cell) IsVisible() bool {
	return !c.Transparent && c.Char != 0 && c.Char != ' '
}

// Equal reports whether two cells are identical in all fields.
func (c Cell) Equal(other Cell) bool {
	return c.Char == other.Char &&
		c.Width == other.Width &&
		c.FG.Equal(other.FG) &&
		c.BG.Equal(other.BG) &&
		c.Attr == other.Attr &&
		c.Transparent == other.Transparent
}
