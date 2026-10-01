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

func TestFormatDurationAvoidsZeroSecondLabel(t *testing.T) {
	if got := FormatDuration(40 * time.Millisecond); got != "<0.1s" {
		t.Fatalf("FormatDuration(40ms) = %q", got)
	}
}

func TestFormatBytes(t *testing.T) {
	for _, test := range []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1024, "1 KB"},
		{1536, "1.5 KB"},
		{10 * 1024, "10 KB"},
		{1024 * 1024, "1 MB"},
		{3 * 1024 * 1024 / 2, "1.5 MB"},
	} {
		if got := FormatBytes(test.bytes); got != test.want {
			t.Fatalf("FormatBytes(%d) = %q, want %q", test.bytes, got, test.want)
		}
	}
}

func TestPascalCaseToolDoesNotFormatStructuredPayload(t *testing.T) {
	if got := PascalCaseTool(`{"handle_id":"agent-8f21"}`); got != "Tool" {
		t.Fatalf("tool name exposed structured payload: %q", got)
	}
}
