package viewmodel

import (
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/ui"
	"crude/workspace"
)

// scripts/tuishot.sh drives the real header and agent disclosure without a
// provider request. F5 opens ordinary tab details; F7 makes the summary ready.
func TestCheckpointStatusVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_CHECKPOINT") != "1" {
		t.Skip("dedicated sequential terminal capture")
	}
	app, ws := newRealShellApp(t)
	app.Tree.Visible = false
	app.AddOrchestrator("root")
	node := ws.AgentMgr.Get("root")
	node.Model, node.Label = "gpt-6.1-sol", "Main Agent"
	node.State.Messages.AppendMessage(workspace.Message{
		Role: workspace.MessageRoleAssistant, Content: "Background context preparation does not block agent work. Its explanation belongs in agent details, not the telemetry header.",
	})
	app.syncAgentMessages(node.ID, false)
	ws.ApplyAgentEvent(workspace.AgentEvent{AgentID: node.ID, Type: "context_precompaction", Data: map[string]any{"phase": "started", "request_id": "visual-summary"}})
	model := &checkpointStatusVisualModel{App: app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type checkpointStatusVisualModel struct{ *App }

func (*checkpointStatusVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *checkpointStatusVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF5:
			m.openTabStats("agent:root")
			m.forceFrame()
			return m, nil
		case tea.KeyF7:
			m.ws.ApplyAgentEvent(workspace.AgentEvent{AgentID: "root", Type: "context_precompaction", Data: map[string]any{"phase": "ready", "request_id": "visual-summary"}})
			m.forceFrame()
			return m, nil
		case tea.KeyF9:
			m.overlayHost.Close(ui.OverlayTabStats)
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
