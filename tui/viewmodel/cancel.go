package viewmodel

import (
	tea "charm.land/bubbletea/v2"
)

// handleCancel runs the composer's Esc ladder. Esc frees the text area before
// it cancels anything: an active selection is dropped first, then a visible
// command list closes, then the draft is cleared, and only then does Esc
// cancel the turn of the selected agent, the one whose tab or pane has focus.
// Agents in other tabs and panes keep running: each is stopped from its own
// pane or with Stop current run. Esc with nothing to stop is a no-op: it
// reports nothing and never re-enters a stopping boundary.
func (m *App) handleCancel() tea.Cmd {
	if m.Input.HasInputSelection() {
		m.Input.ClearInputSelection()
		return nil
	}
	if m.Input.DismissSlashSuggestions() {
		return nil
	}
	if m.Input.Value() != "" {
		m.Input.ClearDraft()
		return nil
	}
	if n := m.AgentView.ActiveNode(); n != nil {
		m.cancelAgentTurn(n.ID)
	}
	return nil
}

// canCancelAgent is the single cancel precondition for every entry point
// (Esc, palette, menus, prompts): live work, or a recovery parked for retry.
func (m *App) canCancelAgent(agentID string) bool {
	node := m.ws.AgentMgr().Get(agentID)
	return node != nil && node.State.CanStop()
}

// cancelAgentTurn stops agentID's current work and reports whether there was
// any. An idle, stopping or finished agent is left untouched: entering the
// stopping presentation without a run would never receive the terminal
// acknowledgement that clears it.
func (m *App) cancelAgentTurn(agentID string) bool {
	if agentID == "" || !m.canCancelAgent(agentID) {
		return false
	}
	state := m.ws.AgentMgr().Get(agentID).State
	if m.canRetryAgentRecovery(agentID) {
		status := state.RecoveryStatus()
		if cancel := m.ws.PrepareCancelAgentRecovery(agentID, status.ID, status.Generation); cancel != nil {
			m.ws.BeginAgentCancellation(agentID)
			m.dropThinkingPresentation(state)
			m.syncAgentMessages(agentID, true)
			generation := m.workspaceSwitchGen.Load()
			m.runDetachedAsyncCmd(func() tea.Msg {
				return recoveryCancelResultMsg{agentID: agentID, incidentID: status.ID, generation: generation, err: cancel()}
			})
			return true
		}
	}
	m.ws.BeginAgentCancellation(agentID)
	m.dropThinkingPresentation(state)
	m.syncAgentMessages(agentID, true)
	m.ws.CancelAgent(agentID)
	return true
}
