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

func controlsOfKind(screen *Screen, kind int) []*rowControl {
	var out []*rowControl
	for _, child := range screen.Children() {
		if control := child.(*rowControl); control.kind == kind {
			out = append(out, control)
		}
	}
	return out
}

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
			terminate := controlsOfKind(screen, controlTerminate)
			if len(terminate) != 2 {
				t.Fatalf("width=%d terminate buttons=%d, want only the two running rows", width, len(terminate))
			}
			for _, child := range terminate {
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

// Every row offers its command as a link to the conversation and an Output
// button; both target that exact command, finished rows included.
func TestBackgroundJobsCommandLinkAndOutputTargetExactRows(t *testing.T) {
	now := time.Now()
	screen := New(theme.DefaultDark())
	screen.SetJobs([]Job{
		{OwnerID: "root", Handle: "server", Command: "npm run dev", Status: "running", PID: 12345, StartedAt: now.Add(-time.Minute)},
		{OwnerID: "root", Handle: "done", Command: "go test ./...", Status: "completed", PID: 222, StartedAt: now.Add(-2 * time.Second), EndedAt: now},
	}, now)
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(120, 8)
	buffer := surface.NewCellBuffer(120, 8)
	host.Paint(buffer)
	links, outputs := controlsOfKind(screen, controlReveal), controlsOfKind(screen, controlOutput)
	if len(links) != 2 || len(outputs) != 2 {
		t.Fatalf("links=%d outputs=%d, want one of each per row", len(links), len(outputs))
	}
	for index, link := range links {
		bounds, _ := host.ScreenRect(link.ID())
		text := ""
		for x := bounds.X; x < bounds.X+bounds.W; x++ {
			text += string(buffer.Get(x, bounds.Y).Char)
		}
		if !strings.Contains(text, screen.rows[index].Command) {
			t.Fatalf("link %q does not show its command %q", text, screen.rows[index].Command)
		}
		if buffer.Get(bounds.X+1, bounds.Y).Attr&cell.AttrUnderline == 0 {
			t.Fatal("command link is not underlined at rest")
		}
		result := host.Handle(tea.MouseClickMsg{X: bounds.X + 2, Y: bounds.Y, Button: tea.MouseLeft})
		host.Handle(tea.MouseReleaseMsg{X: bounds.X + 2, Y: bounds.Y, Button: tea.MouseLeft})
		if result.Action == nil || result.Action.ID != ActionReveal || result.Action.Payload.(RevealRequest).Job != screen.rows[index] {
			t.Fatalf("command click did not request its own reveal: %+v", result.Action)
		}
		out, _ := host.ScreenRect(outputs[index].ID())
		result = host.Handle(tea.MouseClickMsg{X: out.X + 1, Y: out.Y, Button: tea.MouseLeft})
		host.Handle(tea.MouseReleaseMsg{X: out.X + 1, Y: out.Y, Button: tea.MouseLeft})
		if result.Action == nil || result.Action.ID != ActionOutput || result.Action.Payload.(OutputRequest).Job != screen.rows[index] {
			t.Fatalf("Output did not request its own command: %+v", result.Action)
		}
	}
	// Output buttons share one column even when only one row can terminate.
	a, _ := host.ScreenRect(outputs[0].ID())
	b, _ := host.ScreenRect(outputs[1].ID())
	if a.X != b.X {
		t.Fatalf("Output buttons are not aligned: %d vs %d", a.X, b.X)
	}
}

func TestBackgroundJobsKeyboardWalksTheControlGrid(t *testing.T) {
	screen := New(theme.DefaultDark())
	now := time.Now()
	screen.SetJobs([]Job{{Handle: "first", Command: "sleep 1", Status: "running", StartedAt: now}, {Handle: "second", Command: "sleep 2", Status: "running", StartedAt: now}}, now)
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(120, 8)
	host.Layout()
	first := controlsOfKind(screen, controlTerminate)[0]
	host.Focus(first.ID())
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	second := controlsOfKind(screen, controlTerminate)[1]
	if host.Focused() != second.ID() {
		t.Fatal("Down did not keep the column on the next command")
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action == nil || result.Action.Payload.(TerminateRequest).Job.Handle != "second" {
		t.Fatal("Enter did not target the keyboard-selected command")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	if host.Focused() != controlsOfKind(screen, controlOutput)[1].ID() {
		t.Fatal("Left did not move to the row's Output button")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if host.Focused() != controlsOfKind(screen, controlReveal)[1].ID() {
		t.Fatal("Shift+Tab did not move to the row's command link")
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action == nil || result.Action.ID != ActionReveal {
		t.Fatal("Enter on the command link did not request a reveal")
	}
}

func TestBackgroundJobsClipsToActualViewport(t *testing.T) {
	screen := New(theme.DefaultDark())
	now := time.Now()
	screen.SetJobs([]Job{{Handle: "long-command-handle", Command: "sleep 30", Status: "running", StartedAt: now}}, now)
	for width := 1; width <= 160; width++ {
		for height := 1; height <= 6; height++ {
			host := ui.NewHost(theme.DefaultDark(), ui.NewInset("inset", theme.Insets{Left: 3, Top: 2, Right: 3, Bottom: 2}, screen))
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
