package ui

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/theme"
)

func TestTextTideReversesOnlyBeforeReasoningIsConfirmed(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	crest := func(frame int, forward bool) int {
		for column := 0; column < 40; column++ {
			_, attr := TextTide(tokens.Color(ColorMuted), 0, tokens, frame, column, 40, forward)
			if attr&cell.AttrBold != 0 {
				return column
			}
		}
		t.Fatalf("no visible crest at frame %d forward=%t", frame, forward)
		return -1
	}
	if a, b, c := crest(650, true), crest(1300, true), crest(1950, true); a >= b || b >= c {
		t.Fatalf("settled reasoning reversed its left-to-right motion: %d %d %d", a, b, c)
	}
	if a, b, c := crest(650, false), crest(1300, false), crest(1950, false); a <= b || b >= c {
		t.Fatalf("provisional reasoning stopped its reversible motion: %d %d %d", a, b, c)
	}
}
