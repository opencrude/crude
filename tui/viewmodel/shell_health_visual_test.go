package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/shellfooter"
	"crude/tui/screens/termdock"
	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

// Sequential scripts/tuishot.sh journey over a real connected App/core and PTY.
// F2 verifies cold launcher/tree state; F3/F4 click the production footer pills;
// F9 verifies the completed journey. Every home, repo, core and PTY is test-owned.
func TestShellHealthVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_SHELL_HEALTH") != "1" {
		t.Skip("dedicated connected-shell visual capture")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Avoid host-wide login scripts: the disposable wrapper ignores the manager's
	// -l argument and starts a clean native shell without user/system startup work.
	shellPath := filepath.Join(home, "health-shell")
	if err := os.WriteFile(shellPath, []byte("#!/bin/sh\nexec /bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shellPath)
	t.Setenv("ENV", "")
	t.Setenv("PS1", "health-shell$ ")
	root := newGitRepoFixture(t)
	path := filepath.Join(root, "tracked.txt")
	skillDir := filepath.Join(root, "review-guide")
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: review-guide\ndescription: Local evidence review.\n---\nReview exact evidence.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	seed := workspace.NewWorkspace(root, "", "tester", types.Settings{}, nil)
	seed.Status.Notify(types.NotificationRecord{ID: "visual-notice", Summary: "Recovered notice", Detail: "This notice survived a cold core start.", Source: "acceptance"})
	seed.Close()
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	shell := newRemoteJourneyShell(t, runtime, root)
	if err := runtime.Each(t.Context(), func(w *workspace.Workspace) {
		defaults := w.AppDefaults()
		defaults.Capabilities.Skills["local:review-guide"] = captypes.SkillConfig{ID: "local:review-guide", Name: "review-guide", Path: skillDir, Mode: "manual", Scope: captypes.ScopeWorkspace}
		defaults.Capabilities.Connections["local:docs"] = captypes.ConnectionConfig{ID: "local:docs", Name: "Documentation MCP", Transport: "invalid", Scope: captypes.ScopeWorkspace}
		w.SetAppDefaults(defaults)
	}); err != nil {
		t.Fatal(err)
	}
	// Inspection/configuration is off the actor, exactly once. Home polling below
	// remains passive and never starts transports or provider requests.
	if _, err := runtime.Workspaces()[0].CapabilitySnapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, shell.app, shell.app.beginClientFileTreeLoad())
	runAppCommand(t, shell.app, shell.app.refreshSourceControl())
	runAppCommand(t, shell.app, shell.app.capabilitySnapshotCmd())
	shell.app.Tree.Model.SetDiagnostics(path, 1, 0, 0)
	model := &shellHealthVisualModel{App: shell.app, t: t, path: path}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	shell.app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if !model.finished {
		t.Error("capture did not complete the health journey")
	}
}

type shellHealthVisualModel struct {
	*App
	t        *testing.T
	path     string
	finished bool
	checked  bool
	terminal bool
}

func (m *shellHealthVisualModel) footerClick(label string) tea.Cmd {
	m.View()
	row := rowText(m.App, m.height-1)
	index := strings.Index(row, label)
	if index < 0 {
		m.t.Errorf("footer has no %s target: %q", label, row)
		return nil
	}
	x := len([]rune(row[:index])) + 1
	_, command := m.App.Update(tea.MouseClickMsg{X: x, Y: m.height - 1, Button: tea.MouseLeft})
	return command
}

func (m *shellHealthVisualModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			paint := ansi.Strip(m.App.View().Content)
			if node := m.Tree.Model.NodeByPath(m.path); node == nil || node.GitStatus == 0 || node.DiagErrors != 1 {
				m.t.Error("tree Git/diagnostics were not projected")
			}
			if m.capabilitySnapshot.Error != "" || m.capabilitySnapshot.Generation == 0 {
				m.t.Error("cold capability state failed")
			}
			if m.width >= 140 && (!strings.Contains(paint, "review-guide") || !strings.Contains(paint, "Documentation MCP")) {
				m.t.Error("wide home omitted named capability resources")
			}
			m.checked = true
			return m, nil
		case tea.KeyF3:
			return m, m.footerClick("Notices")
		case tea.KeyF4:
			return m, m.footerClick("Terminals")
		case tea.KeyF9:
			paint := ansi.Strip(m.App.View().Content)
			if !m.checked || !m.terminal || !m.termDock.TerminalActive() || !strings.Contains(paint, "TERMINAL_HEALTH_READY") || strings.Contains(paint, "Recovered notice") {
				m.t.Error("Notices -> Terminal journey did not replace the rendered panel")
			}
			if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" {
				if err := os.WriteFile(path, []byte(fmt.Sprintf("connected-shell health %dx%d; failed=%v\n", m.width, m.height, m.t.Failed())), 0600); err != nil {
					m.t.Error(err)
				}
			}
			m.finished = true
			return m, tea.Quit
		}
	}
	_, command := m.App.Update(message)
	if action, ok := message.(shellfooter.ActionMsg); ok && action.Action == "notices" {
		if m.termDock.ActivePanel() != termdock.PanelNotices || !strings.Contains(ansi.Strip(m.App.View().Content), "Recovered notice") {
			m.t.Error("saved Notices not visible after footer click")
		}
	}
	if created, ok := message.(terminalCreatedMsg); ok && created.err == nil {
		m.terminal = true
	}
	return m, command
}
