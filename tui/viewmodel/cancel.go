package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"
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
		for _, node := range m.AgentMgr.AllNodes() {
			if node != nil && node.IsActive() {
				m.cancelAgentTurn(node.ID)
			}
		}
		m.Feedback.NotifyInfo("Stopping active turns...")
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
	if node := m.AgentMgr.Get(agentID); node != nil && node.State != nil {
		state := node.State
		state.BeginCancellation()
		if state.ThinkingPaneID != "" {
			if pane := m.PaneMgr.Get(state.ThinkingPaneID); pane != nil && pane.Messages != nil {
				pane.Messages.MarkAllThinkingInterrupted()
				m.syncPaneMessages(state.ThinkingPaneID, true)
			}
			m.handleSubPaneCompletion(agentID)
		}
		m.syncAgentMessages(agentID, true)
	}
	clearedBtw := m.CancelAgent(agentID)
	if node := m.AgentMgr.Get(agentID); node != nil && node.State != nil && node.State.Messages != nil {
		node.State.Messages.RemovePendingBtwIDs(clearedBtw)
		m.syncPendingBtwCount()
		m.syncAgentMessages(agentID, true)
	}
}
