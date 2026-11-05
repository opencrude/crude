package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/window"
)

// The repository capture script opens the real tab menu with F2, activates its
// action through the menu, and opens the resulting tree row's menu with F3.
func TestTreeRevealVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_TAB_REVEAL") != "1" {
		t.Skip("dedicated sequential native capture")
	}
	root := t.TempDir()
	target := filepath.Join(root, "src", "nested", "reveal-target.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package nested\n\n// Reveal this file in the explorer.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "active.go")
	if err := os.WriteFile(other, []byte("package main\n\n// This tab stays active.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, ""), root)
	app := shell.app
	runAppCommand(t, app, app.openPaneFiles([]string{target}, app.Win.ActiveGroup()))
	tab := app.Win.TabByTitle(app.editorTabTitle(target))
	runAppCommand(t, app, app.openPaneFiles([]string{other}, app.Win.ActiveGroup()))
	app.setActivity(activityAgents, false)
	app.Tree.Visible = false
	model := &treeRevealVisualModel{App: app, t: t, target: target, tab: tab.ID}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if model.failure != nil || !model.revealed || !model.treeMenu {
		t.Fatalf("visual journey did not finish: revealed=%v tree-menu=%v error=%v", model.revealed, model.treeMenu, model.failure)
	}
}

type treeRevealVisualModel struct {
	*App
	t        *testing.T
	target   string
	tab      window.TabID
	revealed bool
	treeMenu bool
	failure  error
}

func (*treeRevealVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *treeRevealVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			_ = m.View()
			x, y := paneTabPoint(m.t, m.App, m.tab)
			_, cmd := m.App.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
			return m, cmd
		case tea.KeyF3:
			_ = m.View()
			node := m.Tree.Model.SelectedNode()
			if node == nil || node.Path != m.target {
				m.failure = fmt.Errorf("target was not selected")
				return m, nil
			}
			m.revealed = true
			x, y := editorTreePoint(m.t, m.App, filepath.Base(m.target))
			_, cmd := m.App.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
			if _, found := contextMenuItem(m.LastContextMenu, "Reveal in tree"); found || m.LastContextMenu == nil {
				m.failure = fmt.Errorf("tree menu is missing or offers the redundant action")
			} else {
				m.treeMenu = true
			}
			return m, cmd
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
