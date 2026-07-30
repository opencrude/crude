package rendering

import (
	"fmt"
	"image/color"

	"crude/tui/cell"
)

// ThemeColorToHex returns "#rrggbb" for a theme color, or "" if nil/transparent.
func ThemeColorToHex(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return ""
	}
	return fmt.Sprintf("#%02x%02x%02x",
		uint8(float32(r)/float32(a)*255),
		uint8(float32(g)/float32(a)*255),
		uint8(float32(b)/float32(a)*255),
	)
}

// ThemeColorToCell converts a theme color (color.Color) to cell.Color.
// This handles the color.Color interface used by the theme system's
// Colors struct fields (Bg, Fg, Accent, etc.).
//
// If the input is nil or fully transparent (alpha == 0), returns cell.NoColor.
// Otherwise, converts the pre-multiplied RGBA values to non-pre-multiplied
// uint8 RGB and returns a TrueColor cell.Color.
func ThemeColorToCell(c color.Color) cell.Color {
	if c == nil {
		return cell.NoColor
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return cell.NoColor
	}
	return cell.NewTrueColor(
		uint8(float32(r)/float32(a)*255),
		uint8(float32(g)/float32(a)*255),
		uint8(float32(b)/float32(a)*255),
	)
}

// LipglossColorToCell converts a lipgloss color string to cell.Color.
// Lipgloss colors are strings in one of two formats:
//
// - Hex: "#RRGGBB" (7 characters) → TrueColor
// - Palette index: "0"-"255" → PaletteColor
//
// Returns cell.NoColor for empty or unrecognizable strings.
func LipglossColorToCell(s string) cell.Color {
	if s == "" {
		return cell.NoColor
	}
	if len(s) == 7 && s[0] == '#' {
		r := hexToUint8(s[1:3])
		g := hexToUint8(s[3:5])
		b := hexToUint8(s[5:7])
		return cell.NewTrueColor(r, g, b)
	}
	// Try as palette index
	n := 0
	valid := true
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			n = n*10 + int(s[i]-'0')
		} else {
			valid = false
			break
		}
	}
	if valid && n >= 0 && n <= 255 {
		return cell.NewPaletteColor(uint8(n))
	}
	return cell.NoColor
}

// hexToUint8 parses a two-character hexadecimal string as a uint8.
// Returns 0 for invalid input.
func hexToUint8(s string) uint8 {
	var result uint8
	for i := 0; i < len(s) && i < 2; i++ {
		result <<= 4
		switch {
		case s[i] >= '0' && s[i] <= '9':
			result |= uint8(s[i] - '0')
		case s[i] >= 'a' && s[i] <= 'f':
			result |= uint8(s[i]-'a') + 10
		case s[i] >= 'A' && s[i] <= 'F':
			result |= uint8(s[i]-'A') + 10
		}
	}
	return result
}
