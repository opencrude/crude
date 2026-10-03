package backgroundjobs

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestBackgroundJobsTerminateButtonsTargetExactRows(t *testing.T) {
	now := time.Now()
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, width := range []int{42, 80, 120, 160} {
			screen := New(th)
			screen.SetJobs([]Job{
				{OwnerID: "root", Handle: "first", Command: "sleep 30", Status: "running", PID: 12345, StartedAt: now.Add(-time.Minute)},
				{OwnerID: "root", Handle: "second", Command: "sleep 30", Status: "running", PID: 67890, StartedAt: now},
				{OwnerID: "root", Handle: "done", Command: "exit 0", Status: "completed", EndedAt: now},
			}, now)
			host := ui.NewHost(th, screen)
			host.Resize(width, 8)
			buffer := surface.NewCellBuffer(width, 8)
			host.Paint(buffer)
			if len(screen.Children()) != 2 {
				t.Fatalf("width=%d buttons=%d, want only the two running rows", width, len(screen.Children()))
			}
			for _, child := range screen.Children() {
				bounds, ok := host.ScreenRect(child.ID())
				if !ok || bounds.X < 0 || bounds.X+bounds.W > width || bounds.Y >= 8 {
					t.Fatalf("width=%d invalid button bounds %+v", width, bounds)
				}
				result := host.Handle(tea.MouseClickMsg{X: bounds.X + 1, Y: bounds.Y, Button: tea.MouseLeft})
				if result.Action == nil || result.Action.ID != ActionTerminate {
					t.Fatalf("width=%d click did not activate button: %+v", width, result)
				}
				request := result.Action.Payload.(TerminateRequest)
				if request.Target != screen || request.Job != screen.buttonJobs[child.ID()] {
					t.Fatalf("button requested the wrong command: %+v", request)
				}
				host.Handle(tea.MouseReleaseMsg{X: bounds.X + 1, Y: bounds.Y, Button: tea.MouseLeft})
				if semantics := child.Semantics(); !strings.Contains(semantics.Label, request.Job.Handle) {
					t.Fatalf("compact button lacks command identity: %+v", semantics)
				}
			}
		}
	}
}

func TestBackgroundJobsKeyboardCanReachEveryTerminateButton(t *testing.T) {
	screen := New(theme.DefaultDark())
	now := time.Now()
	screen.SetJobs([]Job{{Handle: "first", Status: "running", StartedAt: now}, {Handle: "second", Status: "running", StartedAt: now}}, now)
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(120, 8)
	host.Layout()
	host.Focus(screen.Children()[0].ID())
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if host.Focused() != screen.Children()[1].ID() {
		t.Fatal("Down did not focus the next command's button")
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action == nil || result.Action.Payload.(TerminateRequest).Job.Handle != "second" {
		t.Fatal("Enter did not target the keyboard-selected command")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if host.Focused() != screen.Children()[0].ID() {
		t.Fatal("Shift+Tab did not focus the previous command's button")
	}
}

func TestBackgroundJobsClipsToActualViewport(t *testing.T) {
	screen := New(theme.DefaultDark())
	now := time.Now()
	screen.SetJobs([]Job{{Handle: "long-command-handle", Command: "sleep 30", Status: "running", StartedAt: now}}, now)
	for width := 1; width <= 160; width++ {
		for height := 1; height <= 6; height++ {
			host := ui.NewHost(theme.DefaultDark(), ui.NewInset("inset", ui.Insets{Left: 3, Top: 2, Right: 3, Bottom: 2}, screen))
			host.Resize(width+6, height+4)
			buffer := surface.NewCellBuffer(width+6, height+4)
			buffer.Fill(cell.NewCell('~', cell.NoColor, cell.NoColor, 0))
			host.Paint(buffer)
			for row := 2; row < height+2; row++ {
				if buffer.Get(2, row).Char != '~' || buffer.Get(width+3, row).Char != '~' {
					t.Fatalf("width=%d height=%d paints outside column", width, height)
				}
			}
			for column := 3; column < width+3; column++ {
				if buffer.Get(column, height+2).Char != '~' {
					t.Fatalf("width=%d height=%d paints below viewport", width, height)
				}
			}
		}
	}
}
