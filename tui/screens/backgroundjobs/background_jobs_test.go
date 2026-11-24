package backgroundjobs

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/command"
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
			for _, want := range []string{"Background jobs", "JOB", "Output"} {
				if !strings.Contains(painted, want) {
					t.Fatalf("size=%#v missing %q:\n%s", size, want, painted)
				}
			}
			if size.W >= 60 {
				for _, want := range []string{"STATUS", "HANDLE", "Running", "[build]", "TIME", "go test ./..."} {
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
	for _, want := range []string{"✓ Completed", "✗ Failed · Exit status 1", "! Stopped"} {
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

func TestActivationHasProgressAndExactControlsWithoutProcessTelemetry(t *testing.T) {
	now := time.Now()
	for _, width := range []int{42, 80, 160} {
		screen := New(theme.DefaultDark())
		job := Job{OwnerID: "alice", Handle: "auto-3", TaskID: "activation-1", Command: "Activate Maps", StartedAt: now, Status: "running", Progress: command.Progress{Phase: "Connecting"}}
		screen.SetJobs([]Job{job}, now)
		host := ui.NewHost(theme.DefaultDark(), screen)
		host.Resize(width, 8)
		buf := surface.NewCellBuffer(width, 8)
		host.Paint(buf)
		row := strings.Split(bufferText(buf), "\n")[4]
		// Connecting never suggests measurable progress, even with room.
		if strings.Contains(row, "──") || !strings.Contains(row, "◜ Connecting") || !strings.Contains(row, "Output") || (!strings.Contains(row, "Stop") && !strings.Contains(row, string(ui.FirstRune(screen.tokens.Glyphs().Close, '✕')))) {
			t.Fatalf("%d missing controls or progress: %s", width, row)
		}
		for _, control := range screen.controls {
			if control.job.TaskID != job.TaskID {
				t.Fatal("activation control lost its lifetime")
			}
		}
		job.Status = "completed"
		job.EndedAt = now
		screen.SetJobs([]Job{job}, now)
		host.MarkLayout()
		host.Paint(buf)
		row = strings.Split(bufferText(buf), "\n")[4]
		if strings.Contains(row, "──") || strings.Contains(row, "Stop") || !strings.Contains(row, "Output") {
			t.Fatalf("settled controls: %s", row)
		}
	}
}

func BenchmarkJobAnimationTick(b *testing.B) {
	now := time.Now()
	s := New(theme.DefaultDark())
	jobs := make([]Job, 200)
	for i := range jobs {
		jobs[i] = Job{StartedAt: now.Add(-time.Duration(i) * time.Second), Status: "running"}
	}
	s.SetJobs(jobs, now)
	b.ReportAllocs()
	for b.Loop() {
		s.Refresh(now)
	}
}

func TestJobAnimationsFollowPhaseWithoutMovingLabels(t *testing.T) {
	now := time.Now()
	jobs := []Job{
		{Handle: "shell", Command: "go test", StartedAt: now, Status: "running"},
		{Handle: "connect", TaskID: "connect", Command: "MCP server", StartedAt: now.Add(time.Second), Status: "running", Progress: command.Progress{Phase: "Connecting"}},
		{Handle: "skill", TaskID: "skill", Command: "Skill files", StartedAt: now.Add(2 * time.Second), Status: "running", Progress: command.Progress{Phase: "Downloading skill", Current: 9, Total: 100}},
		{Handle: "unknown", TaskID: "unknown", Command: "Other files", StartedAt: now.Add(3 * time.Second), Status: "running", Progress: command.Progress{Phase: "Downloading skill", Current: 9}},
		{Handle: "verify", TaskID: "verify", Command: "Review guide", StartedAt: now.Add(4 * time.Second), Status: "running", Progress: command.Progress{Phase: "Verifying skill files"}},
		{Handle: "install", TaskID: "install", Command: "Install guide", StartedAt: now.Add(5 * time.Second), Status: "running", Progress: command.Progress{Phase: "Installing"}},
		{Handle: "list", TaskID: "list", Command: "List tools", StartedAt: now.Add(6 * time.Second), Status: "running", Progress: command.Progress{Phase: "Discovering MCP tools"}},
		{Handle: "done", TaskID: "done", Command: "Previous job", StartedAt: now.Add(7 * time.Second), Status: "completed", EndedAt: now},
	}
	for _, width := range []int{80, 160} {
		th := theme.DefaultDark()
		screen := New(th)
		screen.SetJobs(jobs, now)
		host := ui.NewHost(th, screen)
		host.Resize(width, 12)
		buf := surface.NewCellBuffer(width, 12)
		paint := func(frame int) []string {
			host.SetFrame(frame)
			host.Paint(buf)
			return strings.Split(bufferText(buf), "\n")
		}
		first := paint(0)
		status := strings.Index(first[2], "STATUS")
		for _, step := range []struct {
			frame    int
			arc, dot rune
		}{{0, '◜', '●'}, {160, '◝', '●'}, {320, '◞', '●'}, {400, '◞', '○'}, {480, '◟', '○'}, {800, '◝', '●'}} {
			lines := paint(step.frame)
			for y, want := range []rune{step.dot, step.arc, step.arc, step.arc, step.dot, step.dot, step.dot, '✓'} {
				y += 4
				row, baseline := []rune(lines[y]), []rune(first[y])
				if row[status] != want || string(row[status+1:]) != string(baseline[status+1:]) {
					t.Fatalf("%d: frame %d: want %c and the same label and controls: %s", width, step.frame, want, lines[y])
				}
				if y != 6 && (strings.Contains(lines[y], "──") || strings.ContainsRune(lines[y], '%')) {
					t.Fatalf("%d: row without a measured download painted progress: %s", width, lines[y])
				}
			}
		}
		before := paint(0)
		jobs[2].Progress.Current = 48
		screen.SetJobs(jobs, now)
		host.MarkLayout()
		after := paint(0)
		jobs[2].Progress.Current = 9
		for _, operand := range []string{"STATUS", "TIME", "HANDLE"} {
			if strings.Index(before[2], operand) != strings.Index(after[2], operand) {
				t.Fatalf("%d: %s jumped when progress crossed 10%%:\n%s\n%s", width, operand, before[2], after[2])
			}
		}
		barColumn := func(line string) int {
			text, _, ok := strings.Cut(line, "──")
			if !ok {
				return -1
			}
			return rendering.VisibleWidth(text)
		}
		if !strings.Contains(after[6], "48%") || barColumn(before[6]) != barColumn(after[6]) || width >= 160 && barColumn(after[6]) < 0 {
			t.Fatalf("%d: measured progress was lost or moved the bar:\n%s\n%s", width, before[6], after[6])
		}
		if w := screen.columns.barW; w > 0 {
			filled := 0
			for x := screen.columns.barX; x < screen.columns.barX+w; x++ {
				if buf.Get(x, 6).FG == screen.tokens.Color(ui.ColorAccent2) {
					filled++
				}
			}
			if filled != w*48/100 {
				t.Fatalf("%d: measured fill was %d/%d cells, want 48%%", width, filled, w)
			}
		}
	}
}

func BenchmarkBackgroundJobsAnimationPaint(b *testing.B) {
	now := time.Now()
	th := theme.DefaultDark()
	screen := New(th)
	screen.SetJobs([]Job{
		{Handle: "tests", Command: "go test ./...", StartedAt: now, Status: "running"},
		{Handle: "mcp", TaskID: "mcp", Command: "MCP server", StartedAt: now.Add(time.Second), Status: "running", Progress: command.Progress{Phase: "Connecting"}},
		{Handle: "skill", TaskID: "skill", Command: "Skill files", StartedAt: now.Add(2 * time.Second), Status: "running", Progress: command.Progress{Phase: "Downloading skill", Current: 42, Total: 100}},
	}, now)
	host := ui.NewHost(th, screen)
	host.Resize(120, 8)
	buf := surface.NewCellBuffer(120, 8)
	host.Paint(buf)
	b.ReportAllocs()
	frame := 0
	for b.Loop() {
		frame += 16
		host.SetFrame(frame)
		host.Paint(buf)
	}
}
