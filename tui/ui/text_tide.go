package ui

import (
	"math"

	"crude/tui/cell"
)

const (
	tideTravelMillis = 2400 // Nominal time to cross the visible text.
	tideRadius       = 10.0
)

// TextTideMotion retains the crest's position in cell columns. Live text and
// geometry changes adjust the remaining distance without rephasing the clock.
// The owner advances it once per painted line using the shared millisecond clock.
type TextTideMotion struct {
	center      float64
	frame       int
	initialized bool
}

func (m *TextTideMotion) Advance(frame, width int, forward bool) float64 {
	if width <= 0 {
		*m = TextTideMotion{}
		return -tideRadius
	}
	if !m.initialized || frame < m.frame {
		*m = TextTideMotion{center: -tideRadius, frame: frame, initialized: true}
	}
	elapsed := frame - m.frame
	m.frame = frame
	last := float64(width - 1)
	if !forward {
		phase := float64((frame%tideTravelMillis+tideTravelMillis)%tideTravelMillis) / tideTravelMillis
		m.center = math.Abs(2*phase-1) * last
		return m.center
	}
	// A shorter line can leave the crest entirely offscreen. Reenter from
	// outside the left edge instead of treating the removed columns as time.
	end := last + tideRadius
	if m.center >= end {
		m.center = -tideRadius
	}
	// Entry, traversal and exit all use the same velocity. Very short labels
	// travel at least one highlight's width per nominal traversal.
	speed := math.Max(last, 2*tideRadius) / tideTravelMillis
	m.center += float64(elapsed) * speed
	if m.center >= end {
		m.center = -tideRadius + math.Mod(m.center-end, last+2*tideRadius)
	}
	return m.center
}

// TextTide sweeps a soft highlight through existing grapheme cells. It changes
// ink only, never text, width, background, or source generations. Bold at the
// crest remains legible on terminals that quantize or omit true color.
func TextTide(ink cell.Color, attr cell.Attrs, tokens *TokenSet, center float64, column int) (cell.Color, cell.Attrs) {
	if tokens == nil {
		return ink, attr
	}
	weight := math.Max(0, 1-math.Abs(float64(column)-center)/tideRadius)
	weight = weight * weight * (3 - 2*weight)
	light := tokens.Color(ColorAccent2).Blend(tokens.Color(ColorFg), 0.15)
	if weight > 0.7 {
		attr |= cell.AttrBold
	}
	return ink.Blend(light, float32(weight)), attr
}
