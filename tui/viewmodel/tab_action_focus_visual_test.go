package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/workspace"
)

// scripts/tuishot.sh drives the real pointer/key routes on this isolated shell.
// F2 reopens the fixture tabs, F7 changes theme, and F9 verifies focus and exits.
// No provider requests, real project files, or saved user data are involved.
func TestTabActionFocusVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_TAB_FOCUS") != "1" {
		t.Skip("requires the sequential dedicated-terminal capture workflow")
	}
	app, ws := newRealShellApp(t)
	defer ws.Close()
	for name, text := range map[string]string{
		"main.go":   "package main\n\nfunc main() {\n\tprintln(\"Tab actions return focus to the content\")\n}\n",
		"notes.txt": "Close this file tab, then type directly into the remaining editor.\nThe tab baseline should stay quiet after the operation.\n",
	} {
		if err := os.WriteFile(filepath.Join(app.RootPath, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	model := &tabActionFocusVisualModel{App: app, t: t}
	model.openTabs()
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type tabActionFocusVisualModel struct {
	*App
	t     *testing.T
	light bool
}

func (m *tabActionFocusVisualModel) openTabs() {
	for _, id := range []string{"Alpha", "Beta"} {
		m.AddOrchestrator(id)
		node := m.AgentMgr.Get(id)
		node.Label = id
		if len(node.State.Messages.Messages()) == 0 {
			node.State.Messages.AppendMessage(workspace.Message{
				Role: workspace.MessageRoleAssistant, Content: "Closing an agent tab minimizes it without stopping its work. The remaining content receives keyboard focus, not the tab strip.",
			})
		}
		m.syncAgentMessages(id, false)
	}
	m.syncAgentWindowTabs()
	openEditorTabs(m.t, m.App, "main.go", "notes.txt")
}

func (m *tabActionFocusVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *tabActionFocusVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.openTabs()
			m.forceFrame()
			return m, nil
		case tea.KeyF7:
			m.light = !m.light
			preset := "dark"
			if m.light {
				preset = "light"
			}
			m.ApplyThemePreset(preset)
			return m, nil
		case tea.KeyF9:
			if m.FocusStack.Top() == FocusTabBar {
				m.t.Error("capture ended with stale tab-strip focus")
			}
			return m, tea.Quit
		}
	}
	_, command := m.App.Update(msg)
	return m, command
}
