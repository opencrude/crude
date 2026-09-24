package ui

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/theme"
)

func TestStatusStripColorsReactToStateAndStayReadable(t *testing.T) {
	for _, th := range theme.AllPresets() {
		t.Run(th.Name, func(t *testing.T) {
			tokens := NewTokenSet(th)
			for _, hue := range []SemanticColor{ColorAccent, ColorAccent2, ColorDiffHunk, ColorSuccess, ColorWarning, ColorError} {
				_, idle := tokens.StatusStripColors(hue, false, false)
				_, attention := tokens.StatusStripColors(hue, false, true)
				_, active := tokens.StatusStripColors(hue, true, true)
				if idle.Equal(attention) || idle.Equal(active) || attention.Equal(active) || idle.Equal(tokens.Color(hue)) {
					t.Fatalf("hue %v has static full-strength colors", hue)
				}
				for _, state := range [][2]bool{{false, false}, {false, true}, {true, false}, {true, true}} {
					fg, bg := tokens.StatusStripColors(hue, state[0], state[1])
					if ratio := cell.ContrastRatio(fg, bg); ratio < 4.5 {
						t.Fatalf("hue %v state %v contrast %.2f < 4.5", hue, state, ratio)
					}
				}
			}
		})
	}
}
