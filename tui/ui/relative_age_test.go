package ui

import (
	"testing"
	"time"
)

func TestRelativeAgeIsCompact(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		when time.Time
		want string
	}{
		{time.Time{}, ""},
		{now.Add(time.Minute), "now"},
		{now.Add(-27 * time.Second), "now"},
		{now.Add(-4*time.Minute - 59*time.Second), "4m"},
		{now.Add(-12*time.Hour - 46*time.Minute), "12h"},
		{now.Add(-3 * 24 * time.Hour), "3d"},
		{time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC), "Feb 7"},
		{time.Date(2025, 2, 7, 9, 0, 0, 0, time.UTC), "Feb 2025"},
	}
	for _, c := range cases {
		if got := RelativeAge(c.when, now); got != c.want {
			t.Errorf("RelativeAge(%v) = %q, want %q", c.when, got, c.want)
		}
	}
}
