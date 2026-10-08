package viewmodel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/workspace"
)

// Run only through scripts/tuishot.sh with this isolated test binary. All
// pointer/key behavior is production; F7 changes theme and F9 verifies/exits.
func TestMainPaneVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_MAIN_PANES") != "1" {
		t.Skip("dedicated sequential terminal capture")
	}
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.Tree.Visible = false
	for _, id := range []string{"Alpha", "Beta"} {
		sess := workspace.NewSession(id, t.TempDir(), nil, nil)
		app.SessionsByAgent[id] = sess
		app.AddOrchestrator(id)
		app.AgentMgr.Get(id).Label = id
		app.AgentMgr.Get(id).State.Messages.AppendMessage(workspace.Message{Role: workspace.MessageRoleAssistant, Content: "This is " + id + ". Drag my tab to an edge to split, or to the middle to join a group. My conversation and draft stay mine.", Time: time.Now()})
		app.syncAgentMessages(id, false)
		app.Input.SetInput(id + " draft stays with this session")
	}
	for name, text := range map[string]string{
		"main.go":  "package main\n\n// Drag tabs. The shaded target previews the exact landing pane.\nfunc main() {\n\tprintln(\"Independent editor, shared shell\")\n}\n",
		"notes.md": "# Main view panes\n\n- Drag to left, right, top or bottom\n- Drop in the middle to merge tabs\n- Ctrl+Alt+L / D: empty pane\n- Alt+[ / ]: focus previous / next pane\n- Open File or New Session in an empty pane\n- The plus remains reachable when tabs overflow\n",
	} {
		if err := os.WriteFile(filepath.Join(app.RootPath, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	openEditorTabs(t, app, "main.go", "notes.md")
	model := &mainPaneVisualModel{App: app, t: t}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type mainPaneVisualModel struct {
	*App
	t     *testing.T
	light bool
}

func (*mainPaneVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *mainPaneVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF7:
			m.light = !m.light
			preset := "dark"
			if m.light {
				preset = "light"
			}
			m.ApplyThemePreset(preset)
			return m, nil
		case tea.KeyF9:
			if m.rootHost.HasPointerCapture() {
				m.t.Error("capture left armed")
			}
			if m.FocusStack.Top() == FocusTabBar {
				m.t.Error("tab operation retained strip focus")
			}
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
