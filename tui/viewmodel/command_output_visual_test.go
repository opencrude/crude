package viewmodel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/filetree"
	ctypes "crude/types"
	"crude/workspace"
)

// Sequential scripts/tuishot.sh: F2 expands retained output, F3 hydrates the same
// evidence, F4 switches theme, F9 exits. No provider, user data or process effects.
func TestCommandOutputVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_COMMAND_OUTPUT") != "1" {
		t.Skip("requires the dedicated tuishot terminal")
	}
	root := t.TempDir()
	for _, name := range []string{"config.json", "install.ps1", "module.psm1", "main.lua", "app.vue", "style.css", "LICENSE", "Dockerfile"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	app := newEditorPaintApp(t, root)
	app.width, app.height = 160, 40
	app.sidebarScreen.SetModel(filetree.NewFileTreeModel(root))
	app.ApplyThemePreset("dark")
	app.AddOrchestrator("command-audit")
	state := app.ws.AgentMgr().Get("command-audit").State
	state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleUser, Content: "Audit captured command output and file icons."})
	for i, tc := range []struct {
		tool, cmd, output string
		code              int
	}{
		{"execute_command", "go test ./...", "\x1b[31mFAIL\x1b[0m\r\n    expected: true\n    actual:   false\n    file: /project/parser_test.go:42\n", 7},
		{"read_async_output", "npm run build", "build started\rprogress 50%\rprogress 100%\n\tmodule\tstatus\n\tparser\tOK\n" + strings.Repeat("compile next module\n", 10) + "BUILD SUCCESS\n", 0},
		{"exec_command", "printf '%s' '{\"answer\":42}'", "{\"answer\":42}\n", 0},
	} {
		body, err := json.Marshal(map[string]any{"command": tc.cmd, "exit_code": tc.code, "output_descriptor": map[string]any{"handle": "build", "status": map[bool]string{true: "failed", false: "completed"}[tc.code != 0], "total_bytes": len(tc.output)}, "retrieval": "Model-only retrieval guidance", "output": tc.output})
		if err != nil {
			t.Fatal(err)
		}
		id := []string{"sync", "async", "native"}[i]
		state.ApplyEvent(workspace.AgentEvent{Type: "tool_call", Data: map[string]any{"tool_id": id, "internal_id": id, "name": tc.tool, "args": `{"command":"audit"}`}})
		state.ApplyEvent(workspace.AgentEvent{Type: "tool_result", Data: map[string]any{"tool_id": id, "internal_id": id, "name": tc.tool, "result": string(body)}})
	}
	for i := 0; i < 8; i++ {
		state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second))
	}
	app.syncAgentMessages(state.ID, false)
	model := &commandOutputVisualModel{App: app, owner: state.ID, t: t}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	app.StopAsyncIO()
}

type commandOutputVisualModel struct {
	*App
	owner string
	t     *testing.T
	light bool
}

func (m *commandOutputVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *commandOutputVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.subPaneProjections.GetExisting(m.owner).renderer.ToggleDisclosure("async")
			return m, nil
		case tea.KeyF3:
			state := m.ws.AgentMgr().Get(m.owner).State
			messages := state.Messages.Messages()
			var records []sessiondb.MessageWithMeta
			for _, message := range messages {
				records = append(records, sessiondb.MessageWithMeta{Msg: provider.ChatMessage{Role: message.Role}, Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{message}}})
			}
			m.subPaneProjections.GetExisting(m.owner).renderer.SyncMessages(ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(records)), state.Messages.Version()+1)
			return m, nil
		case tea.KeyF4:
			m.light = !m.light
			preset := "dark"
			if m.light {
				preset = "light"
			}
			m.ApplyThemePreset(preset)
			return m, nil
		case tea.KeyF9:
			if proof := os.Getenv("CRUDE_VISUAL_PROOF"); proof != "" {
				if err := os.WriteFile(proof, []byte("command output visual journey completed\n"), 0600); err != nil {
					m.t.Error(err)
				}
			}
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
