package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/types"
)

type recoveryRetryResultMsg struct {
	agentID, incidentID string
	generation          uint64
	err                 error
}

type recoveryCancelResultMsg recoveryRetryResultMsg

func (m *App) canRetryAgentRecovery(agentID string) bool {
	if m.ws.AgentMgr() == nil {
		return false
	}
	node := m.ws.AgentMgr().Get(agentID)
	if node == nil || node.State == nil || node.State.Cancelling || node.State.Completed {
		return false
	}
	status := node.State.RecoveryStatus()
	return status.CanRetry()
}

func (m *App) retryAgentRecovery(agentID string) tea.Cmd {
	if !m.canRetryAgentRecovery(agentID) {
		if m.Feedback != nil {
			m.Feedback.NotifyInfo("This agent has no pending recovery")
		}
		return nil
	}
	status := m.ws.AgentMgr().Get(agentID).State.RecoveryStatus()
	run := m.ws.PrepareAgentRecovery(agentID, status.ID, status.Generation)
	if run == nil {
		return nil
	}
	generation := m.workspaceSwitchGen.Load()
	return func() tea.Msg {
		return recoveryRetryResultMsg{agentID: agentID, incidentID: status.ID, generation: generation, err: run()}
	}
}

func (m *App) applyRecoveryRetryResult(result recoveryRetryResultMsg) {
	if result.generation != m.workspaceSwitchGen.Load() || m.ws.AgentMgr() == nil || m.Feedback == nil {
		return
	}
	if node := m.ws.AgentMgr().Get(result.agentID); node == nil || node.State == nil ||
		node.State.RecoveryStatus().ID != result.incidentID {
		return
	}
	if result.err != nil {
		m.Feedback.NotifyInfo("Continue was not applied: " + result.err.Error())
	}
	// Success comes from the owner's recovery event, never an optimistic UI phase.
}

func (m *App) applyRecoveryCancelResult(result recoveryCancelResultMsg) {
	if result.generation != m.workspaceSwitchGen.Load() || m.ws.AgentMgr() == nil || result.err == nil {
		return
	}
	node := m.ws.AgentMgr().Get(result.agentID)
	if node == nil || node.State == nil || node.State.RecoveryStatus().ID != result.incidentID {
		return
	}
	m.ws.RejectAgentRecoveryCancellation(result.agentID, result.incidentID)
	if m.Feedback != nil {
		m.Feedback.NotifyInfo("Stop was not applied: " + result.err.Error())
	}
}

func (m *App) openNetworkSettings() {
	m.openSettings(types.SettingsScopeApplication)
	window := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := m.settingsSurface(window)
	if body == nil {
		return
	}
	for _, item := range body.Model().CategoryItems() {
		if item.Label == "Connection" {
			body.Model().SelectCategory(item.Payload.(int))
			body.SetModel(body.Model())
			if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
				frame.Host().MarkLayout()
			}
			window.MarkContentDirty()
			return
		}
	}
}
