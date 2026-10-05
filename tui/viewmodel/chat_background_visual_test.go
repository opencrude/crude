package viewmodel

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	ctypes "crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

// Opt-in production-shell fixture for scripts/tuishot.sh. It uses no provider
// calls or user data. F2 toggles Board disclosure, F3 switches light/dark, F4
// settles thinking, and F9 exits.
func TestChatBackgroundVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_CHAT_BACKGROUNDS") != "1" {
		t.Skip("requires sequential scripts/tuishot.sh in a dedicated terminal")
	}
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	app.width, app.height = 160, 40
	app.AddOrchestrator("background-owner")
	state := app.AgentMgr.Get("background-owner").State
	state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleAssistant,
		Content: "Regular replies stay on the chat background. Special elements form distinct full-width bands."})
	for _, c := range specialChatBackgroundCases()[:3] {
		for _, message := range c.messages {
			state.Messages.AppendMessage(message)
		}
	}
	pane := app.PaneMgr.GetOrCreate("visual-child", workspace.PaneAgent, "Asha · Researcher")
	pane.Messages.AppendMessage(workspace.Message{
		Role: ctypes.RoleTool, ToolName: "read_file", ToolArgs: "parser.go", ToolStatus: ctypes.ToolStatusSuccess,
	})
	app.subPaneProjections.Get(pane.ID, pane.Type)
	app.subPaneProjections.SyncFromWorkspace(pane.ID, pane.Messages, false)
	state.Messages.AppendMessage(workspace.Message{InternalID: "spawn", Role: ctypes.RoleTool, SubPaneID: pane.ID})
	app.syncAgentMessages(state.ID, false)
	app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: state.ID, Type: "thinking",
		Data: reasoning.Delta{Kind: reasoning.Raw,
			Text: strings.Repeat("Inspect the exact source and retained evidence before choosing the authoritative owner.\n", 40)}})
	model := &chatBackgroundVisualModel{App: app, ownerID: state.ID}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type chatBackgroundVisualModel struct {
	*App
	ownerID string
	light   bool
}

func (m *chatBackgroundVisualModel) Init() tea.Cmd { return nil }

func (m *chatBackgroundVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.subPaneProjections.GetExisting(m.ownerID).renderer.ToggleDisclosure("board")
			return m, nil
		case tea.KeyF3:
			m.light = !m.light
			preset := "dark"
			if m.light {
				preset = "light"
			}
			m.ApplyThemePreset(preset)
			return m, nil
		case tea.KeyF4:
			m.handleAgentEvent(ctypes.AgentEventMsg{AgentID: m.ownerID, Type: "done",
				Data: map[string]any{"reason": "completed"}})
			renderer := m.subPaneProjections.GetExisting(m.ownerID).renderer
			for _, message := range m.AgentMgr.Get(m.ownerID).State.Messages.Messages() {
				if message.Role == ctypes.RoleThinking {
					renderer.ToggleDisclosure(message.InternalID)
				}
			}
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
