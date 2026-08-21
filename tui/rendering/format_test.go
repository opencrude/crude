package rendering

import (
	"testing"
	"time"
)

func TestFormatThinkingDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, ""},
		{-time.Second, ""},
		{1500 * time.Millisecond, "1s"},
		{42 * time.Second, "42s"},
		{130 * time.Second, "2m 10s"},
		{3785 * time.Second, "1h 03m 05s"},
	}
	for _, c := range cases {
		if got := FormatThinkingDuration(c.d); got != c.want {
			t.Fatalf("FormatThinkingDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
