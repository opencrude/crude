package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/types"
	"crude/workspace"
)

// This opt-in fixture runs the production shell, compositor, routing, and
// response dispatch in a real terminal. No model requests or user data are used.
// F2 paints one canonical running sub-agent chat row; F4 completes it and paints
// the owner-scoped footer transient. F5 emits two family permissions; F6
// terminates their first owner; F7 terminates the parent; F8 emits a Beta
// permission; F3 opens an editor; F10 opens one root permission to check Deny;
// F1 delivers its backend rejection/completion; F9 exits.
func TestAgentPromptFamilyVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_PROMPTS") != "1" {
		t.Skip("requires the sequential scripts/tuishot.sh workflow and a dedicated terminal")
	}
	app, alpha, beta, _ := newBackendPromptFamilyApp(t)
	app.maybeHydrateComposerDraft()
	app.Input.SetInput("Unfinished prompt: keep this text, cursor and undo history intact.\n  This second line must survive every permission outcome.")
	model := &promptFamilyVisualModel{App: app, t: t, alpha: alpha, beta: beta}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type promptFamilyVisualModel struct {
	*App
	t           *testing.T
	alpha, beta string
	sequence    int
}

func (m *promptFamilyVisualModel) Init() tea.Cmd { return nil }

func (m *promptFamilyVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			root := m.ws.AgentMgr().Get(m.alpha)
			if pane := m.ws.PaneMgr().Get("child-a"); pane != nil {
				pane.Title = workspace.SubAgentActivityLabel("Samuel sub Rebecca", "researcher", "")
			}
			root.State.Messages.UpdateToolCall("visual-spawn", "visual-tool", "agent_start", "Researcher · Inspect renderer and report")
			root.State.Messages.AttachSubPaneToTool("visual-spawn", "child-a", workspace.SubAgentActivityLabel("Samuel sub Rebecca", "researcher", "Inspect renderer and report"))
			m.syncAgentMessages(m.alpha, false)
			return m, nil
		case tea.KeyF4:
			m.handleSubAgentCompleted(workspace.AgentEvent{Type: "sub_agent_completed", Data: map[string]string{
				"agent_id": "child-a", "agent_name": "Samuel sub Rebecca", "category": "researcher", "status": "completed",
			}})
			return m, nil
		case tea.KeyF5:
			m.sequence++
			emitTestPermission(m.App, "child-a", fmt.Sprintf("a-%d", m.sequence))
			emitTestPermission(m.App, "child-b", fmt.Sprintf("b-%d", m.sequence))
			return m, nil
		case tea.KeyF6:
			for _, node := range m.ws.AgentMgr().AllNodes() {
				if node.Label == "child-a" {
					m.cleanupAgent(node.ID)
					break
				}
			}
			m.openNextAgentPrompt()
			return m, nil
		case tea.KeyF7:
			m.cleanupAgent(m.alpha)
			m.syncAgentWindowTabs()
			m.handleTabSwitch()
			return m, nil
		case tea.KeyF8:
			m.sequence++
			emitTestPermission(m.App, m.beta, fmt.Sprintf("beta-%d", m.sequence))
			return m, nil
		case tea.KeyF10:
			m.switchToAgentView(m.alpha)
			m.ws.AgentMgr().Get(m.alpha).State.Messages.UpdateToolCall("visual-deny", "visual-deny", "edit_file", "main.go")
			m.handleAgentEvent(types.AgentEventMsg{AgentID: m.alpha, Type: "blocking", Data: map[string]string{
				"block_id": "visual-deny", "operation": "Update", "tool": "edit_file", "target": "main.go",
				"question": "Allow this Update operation on main.go?", "reason": "The edit_file tool modifies files on disk",
			}})
			return m, nil
		case tea.KeyF1:
			for _, event := range []workspace.AgentEvent{
				{Type: "permission_resolved", Data: map[string]any{"internal_id": "visual-deny", "tool_id": "visual-deny", "tool": "edit_file", "decision": "denied"}},
				{Type: "blocking_complete", Data: map[string]string{"block_id": "visual-deny"}},
				{Type: "tool_progress", Data: map[string]any{"internal_id": "visual-deny", "tool_id": "visual-deny", "name": "edit_file", "phase": "error"}},
				{Type: "tool_result", Data: map[string]any{"internal_id": "visual-deny", "tool_id": "visual-deny", "name": "edit_file", "result": "The user denied this edit. No file was changed.", "error": "true"}},
				{Type: "phase", Data: map[string]any{"name": "waiting_provider", "provider": "Local fixture"}},
			} {
				m.handleAgentEvent(types.AgentEventMsg{AgentID: m.alpha, Type: event.Type, Data: event.Data})
			}
			return m, nil
		case tea.KeyF3:
			command, err := m.openEditorTab(filepath.Join(m.ws.RootPath(), "main.go"))
			if err != nil {
				m.t.Fatal(err)
			}
			return m, command
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, command := m.App.Update(msg)
	return m, command
}
