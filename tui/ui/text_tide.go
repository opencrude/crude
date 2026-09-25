package ui

import (
	"math"

	"crude/tui/cell"
)

// TextTide sweeps a soft highlight through existing grapheme cells. It changes
// ink only, never text, width, background, or source generations. Bold at the
// crest remains legible on terminals that quantize or omit true color.
func TextTide(ink cell.Color, attr cell.Attrs, tokens *TokenSet, frame, column, width int, forward bool) (cell.Color, cell.Attrs) {
	if tokens == nil || width <= 0 {
		return ink, attr
	}
	const radius = 10.0
	phase := float64((frame%2600+2600)%2600) / 2600
	// Keep the crest on visible ink throughout the cycle. Unclassified text
	// reverses at the edges; a confirmed summary restarts from the left.
	center := math.Abs(2*phase-1) * float64(width-1)
	if forward {
		center = phase * float64(width-1)
	}
	weight := math.Max(0, 1-math.Abs(float64(column)-center)/radius)
	weight = weight * weight * (3 - 2*weight)
	light := tokens.Color(ColorAccent2).Blend(tokens.Color(ColorFg), 0.15)
	if weight > 0.7 {
		attr |= cell.AttrBold
	}
	return ink.Blend(light, float32(weight)), attr
}
