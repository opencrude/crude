package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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
	app, _ := newRealShellApp(t)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	t.Setenv("PS1", "snapshot-shell$ ")
	t.Setenv("CRUDE_TERMINFO_DIR", filepath.Join(t.TempDir(), "terminfo"))
	app.Tree.Visible = false
	model := &terminalSnapshotVisualModel{App: app, t: t, sentinel: artifact}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if !model.finished {
		t.Error("capture exited without completing terminal acceptance")
	}
}

type terminalSnapshotVisualModel struct {
	*App
	t                                            *testing.T
	sentinel                                     string
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
	paint := ansi.Strip(m.App.View().RenderContent())
	if !strings.Contains(text, "SNAPSHOT_READY") || !strings.Contains(paint, "SNAPSHOT_READY") {
		m.t.Error("real PTY output marker missing from snapshot or painted App")
		return false
	}
	if m.latest.Error != "" || m.latest.Generation == 0 || !m.latest.Info.Alive {
		m.t.Error("terminal projection is failed, empty or exited")
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
				m.t.Error("new terminal did not own keyboard focus")
			}
			if m.terminalClipboard[m.latest.Info.ID] != "SNAPSHOT_CLIPBOARD" {
				m.t.Error("OSC 52 offer did not reach the explicit-copy policy")
			}
			return m, nil
		case tea.KeyF4:
			m.handleTerminalAction(ui.Action{ID: shellterm.ActionTakeControl, Payload: m.latest.Info.ID})
			paint := ansi.Strip(m.App.View().RenderContent())
			m.menu = strings.Contains(paint, "Take input control") && strings.Contains(paint, "Copy offered text")
			if !m.menu {
				m.t.Error("production terminal control/clipboard menu not painted")
			}
			return m, nil
		case tea.KeyF8:
			m.resized = m.checkpoint && m.latest.Grid.GridRows != m.retained.Grid.GridRows
			if !m.resized {
				m.t.Error("pointer dock resize did not reach service snapshot")
			}
			if terminalVisualText(m.retained) != m.retainedText {
				m.t.Error("retained immutable snapshot changed after resize")
			}
			return m, nil
		case tea.KeyF9:
			m.checkOutput()
			if !m.created || !m.checkpoint || !m.menu || !m.resized || !m.terminalDockOwnsKeys() {
				m.t.Error("terminal journey incomplete or terminal focus not restored")
			}
			if !m.t.Failed() {
				body := fmt.Sprintf("PASS grid=%dx%d terminal=%s generation=%d rows=%d->%d marker=SNAPSHOT_READY clipboard=SNAPSHOT_CLIPBOARD retained=unchanged\n", m.width, m.height, m.latest.Info.ID, m.latest.Generation, m.retained.Grid.GridRows, m.latest.Grid.GridRows)
				if err := os.WriteFile(m.sentinel, []byte(body), 0o600); err != nil {
					m.t.Error(err)
				} else {
					m.finished = true
				}
			}
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
	return m, cmd
}
