package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/shellport"
	"crude/tui/screens/shellterm"
	"crude/tui/ui"
	"crude/workspace"
)

// This opt-in fixture is driven sequentially by scripts/tuishot.sh. It uses a
// real App, Program, /bin/sh PTY and isolated HOME/project, without providers or
// saved sessions. F2 checkpoints output; F4 opens the production control menu;
// F8 checks a dock resize; F9 verifies and writes the completion sentinel.
func TestTerminalSnapshotVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_TERMINAL_SNAPSHOTS") != "1" {
		return // Ordinary package tests must not open desktop windows.
	}
	artifact := os.Getenv("CRUDE_TERMINAL_VISUAL_SENTINEL")
	if artifact == "" {
		t.Fatal("capture requires a unique completion sentinel path")
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://unused.invalid"), t.TempDir())
	app := shell.app
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	t.Setenv("PS1", "snapshot-shell$ ")
	t.Setenv("CRUDE_TERMINFO_DIR", filepath.Join(t.TempDir(), "terminfo"))
	app.Tree.Visible = false
	model := &terminalSnapshotVisualModel{App: app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	_, runErr := program.Run()
	app.StopAsyncIO()
	if runErr != nil {
		t.Error(runErr)
	}
	for _, failure := range model.failures {
		t.Error(failure)
	}
	if !model.finished {
		t.Error("capture exited without completing terminal acceptance")
	}
	if !t.Failed() {
		body := fmt.Sprintf("PASS grid=%dx%d terminal=%s generation=%d rows=%d->%d marker=SNAPSHOT_READY clipboard=SNAPSHOT_CLIPBOARD retained=unchanged\n", model.width, model.height, model.latest.Info.ID, model.latest.Generation, model.retained.Grid.GridRows, model.latest.Grid.GridRows)
		if err := os.WriteFile(artifact, []byte(body), 0600); err != nil {
			t.Error(err)
		}
	}
}

type terminalSnapshotVisualModel struct {
	*App
	failures                                     []string
	latest, retained                             workspace.TerminalProjection
	retainedText                                 string
	created, checkpoint, menu, resized, finished bool
}

func (*terminalSnapshotVisualModel) Init() tea.Cmd { return tickCmd() }

func terminalVisualText(p workspace.TerminalProjection) string {
	var out strings.Builder
	for _, row := range p.Grid.Cells {
		for _, c := range row {
			out.WriteString(c.Text)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

func (m *terminalSnapshotVisualModel) checkOutput() bool {
	text := terminalVisualText(m.latest)
	paint := bufferPlain(m.mainSurf.Buffer())
	if !strings.Contains(text, "SNAPSHOT_READY") || !strings.Contains(paint, "SNAPSHOT_READY") {
		m.failures = append(m.failures, "real PTY output marker missing from snapshot or painted App")
		return false
	}
	if m.latest.Error != "" || m.latest.Generation == 0 || !m.latest.Info.Alive {
		m.failures = append(m.failures, "terminal projection is failed, empty or exited")
		return false
	}
	return true
}

func (m *terminalSnapshotVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.checkpoint = m.checkOutput()
			m.retained = m.latest
			m.retainedText = terminalVisualText(m.retained)
			if !m.terminalDockOwnsKeys() {
				m.failures = append(m.failures, "new terminal did not own keyboard focus")
			}
			if m.terminalClipboard[m.latest.Info.ID] != "SNAPSHOT_CLIPBOARD" {
				m.failures = append(m.failures, "OSC 52 offer did not reach the explicit-copy policy")
			}
			return m, nil
		case tea.KeyF4:
			m.handleTerminalAction(ui.Action{ID: shellterm.ActionTakeControl, Payload: m.latest.Info.ID})
			// Opening is checked from the menu's authoritative state. The
			// capture reviews the following presented frame; never call View
			// here and consume its incremental output before Bubble Tea.
			m.menu = m.LastContextMenu != nil && m.overlayHost.IsOpen(ui.OverlayContextMenu)
			if m.menu {
				labels := make(map[string]bool)
				for _, section := range m.LastContextMenu.Sections {
					for _, item := range section.Items {
						labels[item.Label] = true
					}
				}
				m.menu = labels["Paste"] && labels["Rename terminal"] && labels["Copy offered text"] && !labels["Take input control"]
			}
			if !m.menu {
				m.failures = append(m.failures, "production terminal control/clipboard menu not painted")
			}
			return m, nil
		case tea.KeyF8:
			m.resized = m.checkpoint && m.latest.Grid.GridRows != m.retained.Grid.GridRows
			if !m.resized {
				m.failures = append(m.failures, "pointer dock resize did not reach service snapshot")
			}
			if terminalVisualText(m.retained) != m.retainedText {
				m.failures = append(m.failures, "retained immutable snapshot changed after resize")
			}
			return m, nil
		case tea.KeyF9:
			m.checkOutput()
			if !m.created || !m.checkpoint || !m.menu || !m.resized || !m.terminalDockOwnsKeys() {
				m.failures = append(m.failures, "terminal journey incomplete or terminal focus not restored")
			}
			m.finished = true
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	if created, ok := msg.(terminalCreatedMsg); ok && created.err == nil {
		m.created = true
	}
	if result, ok := msg.(terminalProjectionMsg); ok && result.err == nil && result.projection.Generation >= m.latest.Generation {
		m.latest = result.projection
	}
	if frame, ok := msg.(shellport.TerminalFrameMsg); ok && frame.Err == nil && frame.Projection.Generation >= m.latest.Generation {
		m.latest = frame.Projection
	}
	return m, cmd
}
