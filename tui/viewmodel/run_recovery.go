package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/types"
	"crude/types/recovery"
)

type recoveryRetryResultMsg struct {
	agentID, incidentID string
	generation          uint64
	accepted            bool
}

type recoveryCancelResultMsg recoveryRetryResultMsg

func (m *App) canRetryAgentRecovery(agentID string) bool {
	if m.AgentMgr == nil {
		return false
	}
	node := m.AgentMgr.Get(agentID)
	if node == nil || node.State == nil {
		return false
	}
	status := node.State.RecoveryStatus()
	return status.ID != "" && (status.State == recovery.NeedsAttention || status.State == recovery.Interrupted || status.State == recovery.Recovering)
}

func (m *App) retryAgentRecovery(agentID string) tea.Cmd {
	if !m.canRetryAgentRecovery(agentID) {
		if m.Feedback != nil {
			m.Feedback.NotifyInfo("This agent has no pending recovery")
		}
		return nil
	}
	status := m.AgentMgr.Get(agentID).State.RecoveryStatus()
	run := m.PrepareAgentRecovery(agentID, status.ID, status.Generation)
	if run == nil {
		return nil
	}
	generation := m.workspaceSwitchGen.Load()
	return func() tea.Msg {
		return recoveryRetryResultMsg{agentID: agentID, incidentID: status.ID, generation: generation, accepted: run()}
	}
}

func (m *App) applyRecoveryRetryResult(result recoveryRetryResultMsg) {
	if result.generation != m.workspaceSwitchGen.Load() || m.AgentMgr == nil || m.Feedback == nil {
		return
	}
	if node := m.AgentMgr.Get(result.agentID); node == nil || node.State == nil ||
		node.State.RecoveryStatus().ID != result.incidentID {
		return
	}
	if !result.accepted {
		m.Feedback.NotifyInfo("The recovery changed or is no longer pending; review this agent before continuing")
	}
	// Success comes from the owner's recovery event, never an optimistic UI phase.
}

func (m *App) applyRecoveryCancelResult(result recoveryCancelResultMsg) {
	if result.generation != m.workspaceSwitchGen.Load() || m.AgentMgr == nil || result.accepted {
		return
	}
	node := m.AgentMgr.Get(result.agentID)
	if node == nil || node.State == nil || node.State.RecoveryStatus().ID != result.incidentID {
		return
	}
	node.State.Cancelling = false
	if m.Feedback != nil {
		m.Feedback.NotifyInfo("The recovery changed before Stop was applied; review this agent's current run")
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
