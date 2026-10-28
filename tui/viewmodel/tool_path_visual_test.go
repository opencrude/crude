package viewmodel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/types"
	"crude/workspace"
)

// The dedicated tuishot process uses real app/event rendering and no model
// calls. F2 reveals the retained read output; F9 exits the capture window.
func TestToolPathVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_TOOL_PATHS") != "1" {
		t.Skip("requires sequential scripts/tuishot.sh captures")
	}
	root := t.TempDir()
	app := newWorkspaceSwitchLifecycleApp(t, root)
	app.AddOrchestrator("path-visual")
	state := app.ws.AgentMgr().Get("path-visual").State
	state.Messages.AppendMessage(workspace.Message{Role: types.RoleUser, Content: "Inspect the parser and show the exact retained evidence."})
	for _, tc := range []struct {
		id, name string
		args     map[string]string
		result   string
	}{
		{"read", "read_file", map[string]string{"path": filepath.Join(root, "src", "parser.go")}, "Source evidence: " + filepath.Join(root, "src", "parser.go") + "\nfunc Parse() {}\n"},
		{"grep", "grep_files", map[string]string{"pattern": "Parse", "path": filepath.Join(root, "src")}, "1 match\nsrc/parser.go:1: func Parse() {}"},
		{"move", "move_file", map[string]string{"source": filepath.Join(root, "src", "old.go"), "destination": filepath.Join(root, "src", "new.go")}, "Moved file"},
		{"cmd", "execute_command", map[string]string{"command": "cat " + filepath.Join(root, "src", "parser.go")}, `{"exit_code":0,"output":"completed\n"}`},
	} {
		args, err := json.Marshal(tc.args)
		if err != nil {
			t.Fatal(err)
		}
		state.ApplyEvent(workspace.AgentEvent{Type: "tool_call", Data: map[string]any{"internal_id": tc.id, "tool_id": tc.id, "name": tc.name, "args": string(args)}})
		state.ApplyEvent(workspace.AgentEvent{Type: "tool_result", Data: map[string]any{"internal_id": tc.id, "tool_id": tc.id, "name": tc.name, "result": tc.result}})
	}
	for i := 0; i < 8; i++ {
		state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second))
	}
	app.syncAgentMessages(state.ID, false)
	program := tea.NewProgram(&toolPathVisualModel{App: app, owner: state.ID}, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type toolPathVisualModel struct {
	*App
	owner string
}

func (m *toolPathVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *toolPathVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.subPaneProjections.GetExisting(m.owner).renderer.ToggleDisclosure("read")
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
