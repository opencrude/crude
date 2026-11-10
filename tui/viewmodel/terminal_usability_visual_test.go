package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/internal/testterminal"
	"crude/shellport"
	"crude/terminalvt"
	"crude/tui/cell"
	"crude/tui/screens/shellterm"
	"crude/workspace"
)

// Driven sequentially by scripts/tuishot.ps1. The owned native child prints
// Expo's portable QR fixture over the real PTY; the shell receives real pointer
// and key input. No provider, user's shell profile or project is used.
func TestTerminalUsabilityVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_TERMINAL_USABILITY") != "1" {
		t.Skip("sequential native visual capture")
	}
	var fixture struct{ Output string }
	data, err := os.ReadFile(filepath.Join("tui", "termview", "testdata", "expo_halfblock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://unused.invalid"), t.TempDir())
	app := shell.app
	app.Tree.Visible = false
	app.onboarding.trust.askedOwner = app.ws
	app.onboarding.trust.askedGen = app.workspaceSwitchGen.Load()
	app.ApplyThemePreset("dark")
	output := "\x1b[2J\x1b[H" + strings.ReplaceAll(fixture.Output, "\n", "\r\n") + "QR_READY\r\n"
	command, args := testterminal.Command(t, "emit-after-key", output)
	model := &terminalUsabilityVisualModel{
		App: app, command: command, args: args,
	}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	_, runErr := program.Run()
	app.StopAsyncIO()
	// testing.T writes directly to stdout. Never report while Bubble Tea owns
	// that terminal, including on a failing visual check.
	if runErr != nil {
		t.Error(runErr)
	}
	for _, failure := range model.failures {
		t.Error(failure)
	}
	if !model.finished {
		t.Fatal("terminal visual checks did not finish")
	}
	if !t.Failed() {
		body := fmt.Sprintf("PASS native PTY QR and terminal controls; independent animation changed in %d frames with terminal focus\n", model.animationChanges)
		if err := os.WriteFile(os.Getenv("CRUDE_TERMINAL_USABILITY_SENTINEL"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

type terminalUsabilityVisualModel struct {
	*App
	command          string
	args             []string
	terminal         string
	latest           workspace.TerminalProjection
	failures         []string
	finished         bool
	sampleAnimation  bool
	animationChanges int
	animationCells   []cell.Cell
}

func (*terminalUsabilityVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *terminalUsabilityVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			service, generation, controller := m.terminalService(), m.workspaceSwitchGen.Load(), m.terminalController()
			return m, m.clientIOCommand(context.Background(), func(ctx context.Context) tea.Msg {
				info, err := service.Create(workspace.TerminalCreateOpts{Command: m.command, Args: m.args, Cols: 80, Rows: 24})
				if barrier, ok := service.(shellport.TerminalInventoryBarrier); err == nil && ok {
					err = barrier.AwaitInventory(ctx)
				}
				result := terminalCreatedMsg{service: service, generation: generation, info: info, err: err}
				if err == nil {
					result.lease, result.err = service.Acquire(info.ID, controller)
				}
				return result
			})
		case tea.KeyF3:
			m.termDockHeight = 22
			m.forceFrame()
			return m, nil
		case tea.KeyF4:
			// Print only after the native child is raw/ready and the real
			// resize result has enough rows. Starting with a small dock and
			// immediately printing the QR puts its top half into scrollback.
			if !strings.Contains(terminalVisualText(m.latest), "CHILD_READY") || m.latest.Grid.GridRows < 16 {
				m.failures = append(m.failures, "native child or resized terminal was not ready for QR output")
				return m, nil
			}
			if screen := m.termScreens[m.terminal]; screen != nil {
				return m, m.terminalCommand(shellterm.Command{ID: m.terminal, Kind: shellterm.CommandKey, Key: terminalvt.Key{Code: 'q', Text: "q"}})
			}
			m.failures = append(m.failures, "terminal screen is absent")
			return m, nil
		case tea.KeyF5:
			// Keep both the progress rule and native terminal visible, with
			// input still in the terminal. Only the product clock drives paint.
			m.termDockHeight = 8
			m.launcherScreen.BeginLoad(m.ws.RootPath())
			m.sampleAnimation, m.animationChanges, m.animationCells = true, 0, nil
			m.forceFrame()
			return m, nil
		case tea.KeyF6:
			m.sampleAnimation = false
			if m.animationChanges < 30 || !m.terminalOwnsKeys() {
				m.failures = append(m.failures, fmt.Sprintf("independent animation did not advance with terminal focus: %d frames", m.animationChanges))
			}
			m.launcherScreen.FinishLoad(m.ws.RootPath(), nil, nil)
			m.forceFrame()
			return m, nil
		case tea.KeyF8:
			// View commits incremental terminal output. Calling it from an
			// assertion consumes a frame that Bubble Tea will never present.
			// Observe the last painted cells without running the renderer.
			plain := bufferPlain(m.mainSurf.Buffer())
			if !strings.Contains(plain, "QR_READY") || !strings.Contains(plain, "▄▄▄▄▄▄▄▄") || !strings.Contains(plain, "Terminal 1") {
				m.failures = append(m.failures, "native PTY QR, output marker, or stable terminal name did not paint")
			}
			return m, nil
		case tea.KeyF9:
			m.finished = true
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	if created, ok := msg.(terminalCreatedMsg); ok && created.err == nil {
		m.terminal = created.info.ID
	}
	if result, ok := msg.(terminalProjectionMsg); ok && result.err == nil && result.projection.Generation >= m.latest.Generation {
		m.latest = result.projection
	}
	if frame, ok := msg.(shellport.TerminalFrameMsg); ok && frame.Err == nil && frame.Projection.Generation >= m.latest.Generation {
		m.latest = frame.Projection
	}
	return m, cmd
}

func (m *terminalUsabilityVisualModel) View() tea.View {
	view := m.App.View()
	if m.sampleAnimation {
		if rect, ok := m.rootHost.ScreenRect("launcher/rule"); ok && !rect.Empty() {
			cells := clockRegion(m.App, rect)
			if len(m.animationCells) == len(cells) && !slices.Equal(m.animationCells, cells) {
				m.animationChanges++
			}
			m.animationCells = cells
		}
	}
	return view
}

func TestTerminalVisualFailureIsRetainedUntilTheRendererStops(t *testing.T) {
	model := &terminalUsabilityVisualModel{App: newFullTestApp(t)}
	// This deliberately fails a visual check. It must retain the finding, not
	// fail testing.T (which would write over the live TUI) or write a sentinel.
	model.Update(tea.KeyPressMsg{Code: tea.KeyF8})
	if len(model.failures) != 1 || model.finished {
		t.Fatal("visual failure was not retained")
	}
	_, quit := model.Update(tea.KeyPressMsg{Code: tea.KeyF9})
	if !model.finished || quit == nil || len(model.failures) != 1 {
		t.Fatal("failed visual check did not shut down before reporting")
	}
}
