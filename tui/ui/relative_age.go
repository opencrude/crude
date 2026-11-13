package ui

import (
	"fmt"
	"time"
)

// RelativeAge is the one compact age format for lists: "now", "4m", "12h",
// "3d", then a date. Rows that repeat an age column stay short and aligned;
// detail views show the exact timestamp instead.
func RelativeAge(when, now time.Time) string {
	if when.IsZero() {
		return ""
	}
	elapsed := now.Sub(when)
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed < time.Minute:
		return "now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh", int(elapsed.Hours()))
	case elapsed < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(elapsed.Hours()/24))
	case when.Year() == now.Year():
		return when.Format("Jan 2")
	default:
		return when.Format("Jan 2006")
	}
}
