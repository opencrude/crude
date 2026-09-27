package ui

import (
	"fmt"
	"math"
	"testing"

	"crude/tui/cell"
	"crude/tui/theme"
)

func TestTextTideReversesOnlyBeforeReasoningIsConfirmed(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	var motion TextTideMotion
	crest := func(frame int, forward bool) int {
		center := motion.Advance(frame, 40, forward)
		for column := 0; column < 40; column++ {
			_, attr := TextTide(tokens.Color(ColorActivityText), 0, tokens, center, column)
			if attr&cell.AttrBold != 0 {
				return column
			}
		}
		t.Fatalf("no visible crest at frame %d forward=%t", frame, forward)
		return -1
	}
	motion.Advance(0, 40, true)
	if a, b, c := crest(800, true), crest(1600, true), crest(2400, true); a >= b || b >= c {
		t.Fatalf("settled reasoning reversed its left-to-right motion: %d %d %d", a, b, c)
	}
	if a, b, c := crest(650, false), crest(1300, false), crest(1950, false); a <= b || b >= c {
		t.Fatalf("provisional reasoning stopped its reversible motion: %d %d %d", a, b, c)
	}
}

func TestTextTideForwardLeavesAndEntersGradually(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, width := range []int{1, 6, 40, 100, 160} {
			t.Run(fmt.Sprintf("%s/%d", th.Name, width), func(t *testing.T) {
				tokens := NewTokenSet(th)
				ink := tokens.Color(ColorActivityText)
				var motion TextTideMotion
				previous := make([]cell.Color, width)
				lastLit, wraps := false, 0
				previousCenter := motion.Advance(1700000000013, width, true)
				for frame := 0; frame <= 12000; frame += 16 {
					center := motion.Advance(1700000000013+frame, width, true)
					wrapped := center < previousCenter
					if wrapped {
						wraps++
						if !lastLit {
							t.Fatal("restarted before the crest reached the final character")
						}
						lastLit = false
					}
					for col := 0; col < width; col++ {
						fg, attr := TextTide(ink, cell.AttrItalic, tokens, center, col)
						if attr&cell.AttrItalic == 0 {
							t.Fatal("tide removed the original emphasis")
						}
						if col == width-1 && attr&cell.AttrBold != 0 {
							lastLit = true
						}
						if frame == 0 && (fg != ink || attr != cell.AttrItalic) {
							t.Fatal("first frame appeared at full brightness")
						}
						if frame > 0 && tideColorDelta(previous[col], fg) > 24 {
							t.Fatalf("ink jumped at frame %d col %d (wrapped=%t)", frame, col, wrapped)
						}
						if wrapped && (tideColorDelta(previous[col], ink) > 2 || tideColorDelta(fg, ink) > 2 || attr&cell.AttrBold != 0) {
							t.Fatalf("visible highlight at the wrap: frame %d col %d", frame, col)
						}
						previous[col] = fg
					}
					previousCenter = center
				}
				if wraps < 2 {
					t.Fatalf("only observed %d loops", wraps)
				}
			})
		}
	}
}

func TestTextTideMotionKeepsPaceAtEdgesAndPositionWhenWidthChanges(t *testing.T) {
	var motion TextTideMotion
	before := motion.Advance(0, 80, true)
	step := motion.Advance(16, 80, true) - before
	for frame := 32; frame < 2900; frame += 16 {
		before = motion.center
		after := motion.Advance(frame, 80, true)
		if after >= before && math.Abs(after-before-step) > 1e-9 {
			t.Fatalf("speed changed at frame %d: %f -> %f", frame, before, after)
		}
	}
	motion = TextTideMotion{}
	motion.Advance(1000, 80, true)
	before = motion.Advance(2000, 80, true)
	for _, width := range []int{140, 55, 100} {
		if after := motion.Advance(2000, width, true); after != before {
			t.Fatalf("width %d rephased the crest from %f to %f", width, before, after)
		}
	}
	if after := motion.Advance(2016, 100, true); after <= before || after-before > 1 {
		t.Fatalf("text growth teleported the crest from %f to %f", before, after)
	}
	// A direction change continues from the displayed crest.
	before = motion.Advance(2400, 100, false)
	if after := motion.Advance(2400, 100, true); after != before {
		t.Fatal("confirmation reset the crest")
	}
	// Clock resets, empty text and a later restart all enter from offscreen.
	for _, sample := range []struct{ frame, width int }{{10, 40}, {20, 0}, {30, 40}} {
		if center := motion.Advance(sample.frame, sample.width, true); center != -tideRadius {
			t.Fatalf("restart began on visible text: %f", center)
		}
	}
}

func tideColorDelta(a, b cell.Color) int {
	r := int(math.Abs(float64(a.R) - float64(b.R)))
	g := int(math.Abs(float64(a.G) - float64(b.G)))
	bv := int(math.Abs(float64(a.B) - float64(b.B)))
	return max(r, max(g, bv))
}
