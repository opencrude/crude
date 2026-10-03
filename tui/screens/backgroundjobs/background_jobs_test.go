package backgroundjobs

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestBackgroundJobsPaintsCommandDetailsAcrossThemesAndSizes(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 5, 0, time.UTC)
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 42, H: 8}, {W: 120, H: 16}} {
			screen := New(th)
			screen.SetJobs([]Job{{
				Handle: "build", Command: "go test ./...", PID: 42, Status: "running", StartedAt: now.Add(-5 * time.Second),
			}}, now)
			host := ui.NewHost(th, screen)
			host.Resize(size.W, size.H)
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			painted := bufferText(buf)
			for _, want := range []string{"Background jobs", "STATUS", "HANDLE", "COMMAND", "RUNNING", "[build]"} {
				if !strings.Contains(painted, want) {
					t.Fatalf("size=%#v missing %q:\n%s", size, want, painted)
				}
			}
			if size.W >= 60 {
				for _, want := range []string{"DURATION", "PID", "EXIT", "42", "go test ./..."} {
					if !strings.Contains(painted, want) {
						t.Fatalf("size=%#v missing %q:\n%s", size, want, painted)
					}
				}
			} else if !strings.Contains(painted, "go test") {
				t.Fatalf("size=%#v command column truncated wrongly:\n%s", size, painted)
			}
			if strings.Contains(painted, "❯") || strings.Contains(painted, ">") {
				t.Fatalf("size=%#v painted a marker glyph:\n%s", size, painted)
			}
		}
	}
}

func TestBackgroundJobsPaintsStatusGlyphsAndErrors(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	screen := New(theme.DefaultDark())
	screen.SetJobs([]Job{
		{Handle: "done", Command: "true", Status: "completed", EndedAt: now, ExitCode: 0},
		{Handle: "failed", Command: "false", Status: "failed", EndedAt: now, ExitCode: 1, Error: "exit status 1"},
		{Handle: "kill", Command: "sleep 99", Status: "killed", EndedAt: now, ExitCode: 130},
	}, now)
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(120, 12)
	buf := surface.NewCellBuffer(120, 12)
	host.Paint(buf)
	painted := bufferText(buf)
	for _, want := range []string{"✓ COMPLETED", "✗ FAILED", "! KILLED", "exit status 1"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("missing %q:\n%s", want, painted)
		}
	}
}

func TestBackgroundJobsShrinksColumnsOnNarrowWidths(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 5, 0, time.UTC)
	screen := New(theme.DefaultDark())
	screen.SetJobs([]Job{{Handle: "build", Command: "go test ./...", PID: 42, Status: "running", StartedAt: now.Add(-5 * time.Second)}}, now)
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(42, 8)
	buf := surface.NewCellBuffer(42, 8)
	host.Paint(buf)
	painted := bufferText(buf)
	if strings.Contains(painted, "PID") {
		t.Fatalf("narrow layout should drop PID/EXIT columns:\n%s", painted)
	}
	// Command column still paints (truncated with an ellipsis).
	if !strings.Contains(painted, "go test") {
		t.Fatalf("command truncated incorrectly:\n%s", painted)
	}
}

func TestBackgroundJobsExpiresCompletedRowsAndFlagsFailures(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	screen := New(theme.DefaultDark())
	screen.SetJobs([]Job{
		{Handle: "failed", Command: "false", Status: "failed", EndedAt: now, ExitCode: 1, Error: "exit status 1"},
		{Handle: "done", Command: "true", Status: "completed", EndedAt: now, ExitCode: 0},
	}, now)
	if screen.VisibleCount() != 2 || !screen.NeedsAttention() {
		t.Fatalf("initial state visible=%d attention=%v", screen.VisibleCount(), screen.NeedsAttention())
	}
	if !screen.Refresh(now.Add(completedRetention + time.Second)) {
		t.Fatal("expiry did not invalidate the screen")
	}
	if screen.VisibleCount() != 0 || screen.NeedsAttention() {
		t.Fatalf("expired state visible=%d attention=%v", screen.VisibleCount(), screen.NeedsAttention())
	}
}

func TestBackgroundJobsIsPassiveAndDoesNotClaimInputFocus(t *testing.T) {
	screen := New(theme.DefaultDark())
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(80, 10)
	host.Layout()
	if host.Focused() != ui.RootID {
		t.Fatalf("passive screen claimed focus: %q", host.Focused())
	}
	for _, message := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.PasteMsg{Content: "ignored"},
		tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft},
	} {
		if result := host.Handle(message); result.Handled || result.Action != nil || result.Cmd != nil {
			t.Fatalf("passive screen handled %T: %#v", message, result)
		}
	}
}

func bufferText(buf *surface.CellBuffer) string {
	var result strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			result.WriteRune(r)
		}
		result.WriteByte('\n')
	}
	return result.String()
}
