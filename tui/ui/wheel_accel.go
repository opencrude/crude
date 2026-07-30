package ui

import (
	"math"
	"time"
)

// WheelAccelState implements accelerated wheel line deltas (200ms window, max 3 lines).
type WheelAccelState struct {
	velocity      float64
	lastWheelTime time.Time
}

// WheelLines returns signed line count for one wheel notch (sign -1 up, +1 down).
func (w *WheelAccelState) WheelLines(sign int) int {
	now := time.Now()
	elapsed := now.Sub(w.lastWheelTime)
	if !w.lastWheelTime.IsZero() && elapsed < 200*time.Millisecond {
		accel := 0.3 * (1.0 - float64(elapsed)/(200*float64(time.Millisecond)))
		w.velocity = math.Min(w.velocity+accel, 3.0)
	} else {
		w.velocity = 1.0
	}
	w.lastWheelTime = now
	lines := int(math.Ceil(w.velocity))
	if lines < 1 {
		lines = 1
	}
	return lines * sign
}

func (w *WheelAccelState) WheelUp() int   { return w.WheelLines(-1) }
func (w *WheelAccelState) WheelDown() int { return w.WheelLines(1) }
