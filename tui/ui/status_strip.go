package ui

import "crude/tui/cell"

// StatusStripColors keeps the strip in the active theme's palette instead of
// painting raw semantic foreground colors as full-strength background slabs.
// Real attention and the open panel strengthen the fill; idle controls retain
// distinct colored boundaries without presenting a permanent alarm.
func (ts *TokenSet) StatusStripColors(hue SemanticColor, active, attention bool) (fg, bg cell.Color) {
	weight := float32(0.55)
	if attention {
		weight = 0.75
	}
	if active {
		weight = 0.9
	}
	bg = ts.Color(ColorBg).Blend(ts.Color(hue), weight)
	return cell.ContrastInk(bg), bg
}
