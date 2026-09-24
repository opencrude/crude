package cell

import "math"

// RelativeLuminance linearizes sRGB channels for WCAG contrast calculations.
func RelativeLuminance(c Color) float64 {
	r, g, b := colorToRGB(c)
	linear := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

// ContrastRatio returns the WCAG contrast ratio of two resolved colors.
func ContrastRatio(a, b Color) float64 {
	la, lb := RelativeLuminance(a), RelativeLuminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// ContrastInk chooses black or white, guaranteeing at least 4.5:1 on an
// opaque sRGB fill, including mid-tone colors where luma thresholds fail.
func ContrastInk(bg Color) Color {
	dark, light := NewTrueColor(0, 0, 0), NewTrueColor(255, 255, 255)
	if ContrastRatio(bg, dark) >= ContrastRatio(bg, light) {
		return dark
	}
	return light
}
