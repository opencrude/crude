package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/workspace"
)

func (m *App) handleCancel(msg tea.KeyPressMsg) tea.Cmd {
	if m.AgentMgr.VisibleCount() == 0 && m.EditorState.ActiveView == "" {
		return nil
	}

	if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil && !n.State.Completed && n.State.IsActive() {
		m.cancelAgentTurn(n.ID)
		return nil
	}

	if m.anyActive() {
		if n := m.AgentMgr.ActiveNode(); n != nil {
			m.cancelAgentTurn(n.ID)
			return nil
		}
		m.HardCancel()
		m.StatusBar.SetInfo("Stopping active turns...")
		return nil
	}

	inputText := strings.TrimSpace(m.Input.Value())
	if inputText != "" {
		m.Input.ClearInput()
		return nil
	}

	return nil
}

func (m *App) cancelAgentTurn(agentID string) {
	if agentID == "" {
		return
	}
	m.CancelAgent(agentID)
	if node := m.AgentMgr.Get(agentID); node != nil && node.State != nil {
		node.State.Running = true
		node.State.Cancelling = true
		node.State.Streaming = false
		node.State.Thinking = true
		node.State.Messages.UpdateTurnStatus(workspace.TurnStatus{Phase: "cancelling"})
		m.syncAgentMessages(agentID, true)
	}
	if m.StatusBar != nil {
		m.StatusBar.SetThinkingForAgent(agentID, true, false, false)
	}
}
