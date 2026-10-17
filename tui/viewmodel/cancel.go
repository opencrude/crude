package viewmodel

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// handleCancel runs the composer's Esc ladder. Esc frees the text area before
// it cancels anything: an active selection is dropped first, then the draft is
// cleared, and only then does Esc cancel the selected agent's turn, or else
// every other agent's live work. Cancelling the turn therefore always needs
// the draft out of the way, which is what the user sees as "Esc clears the
// text area". Esc with nothing to stop is a no-op: it reports nothing and
// never re-enters a stopping boundary.
func (m *App) handleCancel() tea.Cmd {
	if m.Input.HasInputSelection() {
		m.Input.ClearInputSelection()
		return nil
	}
	if m.Input.Value() != "" {
		m.Input.ClearInput()
		return nil
	}
	if m.AgentView.VisibleCount() == 0 && m.EditorState.ActiveView == "" {
		return nil
	}
	if n := m.AgentView.ActiveNode(); n != nil && m.cancelAgentTurn(n.ID) {
		return nil
	}
	// Other agents lose only live work; their parked recoveries need an
	// explicit Stop on that agent.
	stopped := 0
	for _, node := range m.ws.AgentMgr().AllNodes() {
		if node != nil && node.State.HasCancellableWork() && m.cancelAgentTurn(node.ID) {
			stopped++
		}
	}
	switch {
	case stopped == 1:
		m.Feedback.NotifyInfo("Stopping 1 running agent...")
	case stopped > 1:
		m.Feedback.NotifyInfo(fmt.Sprintf("Stopping %d running agents...", stopped))
	}
	return nil
}

// canCancelAgent is the single cancel precondition for every entry point
// (Esc, palette, menus, prompts): live work, or a recovery parked for retry.
func (m *App) canCancelAgent(agentID string) bool {
	node := m.ws.AgentMgr().Get(agentID)
	if node == nil || node.State == nil {
		return false
	}
	return node.State.HasCancellableWork() || m.canRetryAgentRecovery(agentID)
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
