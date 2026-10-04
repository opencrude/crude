package viewmodel

import (
	tea "charm.land/bubbletea/v2"
)

// handleCancel runs the composer's Esc ladder. Esc frees the text area before
// it cancels anything: an active selection is dropped first, then the draft is
// cleared, and only then does Esc cancel a running turn (or a whole batch of
// them). Cancelling the turn therefore always needs the draft out of the way,
// which is what the user sees as "Esc clears the text area".
func (m *App) handleCancel() tea.Cmd {
	if m.Input.HasInputSelection() {
		m.Input.ClearInputSelection()
		return nil
	}
	if m.Input.Value() != "" {
		m.Input.ClearInput()
		return nil
	}

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
	m.CancelAgent(agentID)
}
