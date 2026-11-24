package backgroundjobs

import (
	"strings"
	"testing"
	"time"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/command"
)

func TestBackgroundJobsUsesAvailableWidthForHandle(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, width := range []int{120, 160} {
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

func TestMixedJobsLeadWithWorkAndKeepControlsAligned(t *testing.T) {
	now := time.Now()
	for _, width := range []int{68, 80, 120, 160} {
		s := New(theme.DefaultDark())
		s.SetJobs([]Job{{Handle: "tests", Command: "go test ./...", Status: "running", StartedAt: now}, {TaskID: "mcp", Handle: "auto-1", Command: "Desktop tools", Status: "running", Progress: command.Progress{Phase: "Connecting"}, StartedAt: now.Add(time.Second)}, {TaskID: "skill", Handle: "auto-2", Command: "Review guide", Status: "running", Progress: command.Progress{Phase: "Downloading skill", Current: 37, Total: 100}, StartedAt: now.Add(2 * time.Second)}, {Handle: "previous", Command: "go vet", Status: "failed", Error: "exit status 1", EndedAt: now, StartedAt: now.Add(3 * time.Second)}}, now)
		host := ui.NewHost(theme.DefaultDark(), s)
		host.Resize(width, 10)
		buf := surface.NewCellBuffer(width, 10)
		host.Paint(buf)
		lines := strings.Split(bufferText(buf), "\n")
		header := lines[2]
		if !strings.Contains(header, "JOB") || strings.Index(header, "STATUS") <= strings.Index(header, "JOB") || strings.Contains(header, "COMMAND") || strings.Contains(header, "PID") || strings.Contains(header, "EXIT") {
			t.Fatal(header)
		}
		for i, want := range []string{"go test ./...", "Desktop tools", "Review guide", "go vet"} {
			row := lines[i+4]
			if !strings.Contains(row, want) || rendering.VisibleWidth(strings.SplitN(row, "Output", 2)[0]) != rendering.VisibleWidth(strings.SplitN(lines[4], "Output", 2)[0]) {
				t.Fatalf("%d: %s", width, row)
			}
		}
		if !strings.Contains(lines[6], "37%") || strings.Contains(lines[5], "%") || strings.Contains(lines[7], "Stop") {
			t.Fatalf("%d: %s", width, bufferText(buf))
		}
	}
}

// The reported deck: a failed activation whose short name kept a wide JOB
// column while the error that explains it was cut after 34 cells. Widths are
// decided from every visible row together, so each column has one position
// for the whole table, the name takes what it needs and the error gets the
// rest.
func TestColumnsShareWidthAcrossAllVisibleRows(t *testing.T) {
	now := time.Now()
	failure := "server exited before it answered: Error: Missing command.\nserver stderr (tail):\nInstalled 84 packages\nError: Missing command."
	jobs := []Job{
		{Handle: "tests", Command: "go test ./...", Status: "running", StartedAt: now.Add(-time.Minute)},
		{TaskID: "mcp", Handle: "auto-2", Command: "Fixture server", Status: "running", Progress: command.Progress{Phase: "Connecting"}, StartedAt: now.Add(-30 * time.Second)},
		{TaskID: "use", Handle: "auto-1", Command: "Windows computer use", Status: "failed", Error: failure, StartedAt: now.Add(-44 * time.Second), EndedAt: now, Duration: 44 * time.Second},
	}
	for _, width := range []int{80, 134, 160} {
		s := New(theme.DefaultDark())
		s.SetJobs(jobs, now)
		host := ui.NewHost(theme.DefaultDark(), s)
		host.Resize(width, 9)
		buf := surface.NewCellBuffer(width, 9)
		host.Paint(buf)
		lines := strings.Split(bufferText(buf), "\n")
		header := lines[2]
		statusAt := strings.Index(header, "STATUS")
		jobAt := strings.Index(header, "JOB")
		// The name column is as wide as its longest name, not wider.
		if gap := statusAt - jobAt - rendering.VisibleWidth("Windows computer use"); gap != ui.TableMinGap {
			t.Fatalf("%d: JOB column wastes %d cells:\n%s", width, gap-ui.TableMinGap, strings.Join(lines[:7], "\n"))
		}
		// Rows are ordered by start time.
		for row, name := range []string{"go test ./...", "Windows computer use", "Fixture server"} {
			line := []rune(lines[4+row])
			if !strings.HasPrefix(strings.TrimSpace(string(line[jobAt:])), name) {
				t.Fatalf("%d: %q does not start under JOB: %s", width, name, string(line))
			}
			if status, want := line[statusAt], []rune{'●', '✗', '◜'}[row]; status != want {
				t.Fatalf("%d: status of %q does not start under STATUS with %c: %s", width, name, want, string(line))
			}
		}
		failed := lines[5]
		if !strings.Contains(failed, "✗ Failed · Server exited") || strings.Contains(failed, "stderr") {
			t.Fatalf("%d: the failure summary is not its first line: %s", width, failed)
		}
		if width >= 134 && !strings.Contains(failed, "Server exited before it answered: Error: Missing command.") {
			t.Fatalf("%d: the error was cut although the row has room: %s", width, failed)
		}
		column := func(line string) int { return rendering.VisibleWidth(strings.SplitN(line, "Output", 2)[0]) }
		for _, line := range lines[5:7] {
			if column(line) != column(lines[4]) {
				t.Fatalf("%d: Output is not aligned:\n%s", width, strings.Join(lines[4:7], "\n"))
			}
		}
	}
}
