package backgroundjobs

import (
	"strings"
	"testing"
	"time"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestBackgroundJobsUsesAvailableWidthForHandle(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, width := range []int{80, 120, 160} {
		screen := New(theme.DefaultDark())
		screen.SetJobs([]Job{{
			Handle: "workspace-test-suite-long-handle", Command: "go test ./...", Status: "running", StartedAt: now.Add(-time.Minute),
		}}, now)
		host := ui.NewHost(theme.DefaultDark(), screen)
		host.Resize(width, 8)
		buffer := surface.NewCellBuffer(width, 8)
		host.Paint(buffer)
		painted := bufferText(buffer)
		for _, text := range []string{"[workspace-test-suite-long-handle]", "go test ./..."} {
			if !strings.Contains(painted, text) {
				t.Errorf("width=%d truncates %q despite spare space:\n%s", width, text, painted)
			}
		}
	}
}

func TestBackgroundJobsUsesWholeSecondHumanDurations(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		duration time.Duration
		want     string
	}{
		{250 * time.Millisecond, "<1s"},
		{5*time.Second + 950*time.Millisecond, "5s"},
		{2*time.Minute + 5*time.Second, "2m 05s"},
		{time.Hour + 3*time.Minute + 9*time.Second, "1h 03m 09s"},
		{25*time.Hour + 3*time.Minute + 9*time.Second, "25h 03m 09s"},
	} {
		screen := New(theme.DefaultDark())
		screen.SetJobs([]Job{{Handle: "build", Command: "true", Status: "completed", Duration: test.duration, EndedAt: now}}, now)
		host := ui.NewHost(theme.DefaultDark(), screen)
		host.Resize(120, 8)
		buffer := surface.NewCellBuffer(120, 8)
		host.Paint(buffer)
		if painted := bufferText(buffer); !strings.Contains(painted, test.want) {
			t.Errorf("duration=%s missing %q:\n%s", test.duration, test.want, painted)
		}
	}
}
