package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/shellport"
)

// applyReplicaChanges is the sole connected-state installation path. Core
// reductions are presentation evidence, never ingress for another reducer.
func (m *App) applyReplicaChanges(msg shellport.ReplicaChangesMsg) tea.Cmd {
	if msg.Owner != m.ws {
		return nil
	}
	replica, ok := m.ws.(shellport.Replica)
	if !ok {
		return nil
	}
	applied := replica.ApplyReplicaChanges(msg)
	var commands []tea.Cmd
	for i, id := range applied.Attached {
		activate := applied.Initial && i == 0
		if m.pendingOrchestrators[id] {
			activate = true
			delete(m.pendingOrchestrators, id)
		}
		m.addOrchestrator(id, activate)
	}
	for id := range m.pendingOrchestrators {
		if m.ws.AgentMgr().Get(id) != nil {
			delete(m.pendingOrchestrators, id)
			m.addOrchestrator(id, true)
		}
	}
	for _, notice := range applied.Notices {
		commands = append(commands, m.presentAgentNotice(notice))
	}
	for _, reduction := range applied.Reductions {
		commands = append(commands, m.presentAgentReduction(reduction))
	}
	for _, follow := range applied.Follow {
		_, command := m.update(follow)
		commands = append(commands, command)
	}
	if m.ActiveSession != nil && m.ws.SessionsByID()[m.ActiveSession.SessionID] != m.ActiveSession {
		m.ActiveSession = nil
	}
	m.syncAgentWindowTabs()
	m.refreshActivityList()
	m.drainLSPDiagnostics()
	if applied.SessionsChanged && m.ActiveSession != nil {
		// Background session/reference publications cannot rebuild a retained
		// editable deck. Opening or explicit save/reset owns draft installation.
		projection := m.activeSessionProjection()
		editorOpen := false
		if projection != nil {
			for _, panel := range projection.deck.Panels() {
				editorOpen = editorOpen || panel.ID == sessionSettingsPanelID
			}
		}
		if !editorOpen {
			m.refreshSessionSettingsProjection(m.ActiveSession)
		}
	}
	return tea.Batch(commands...)
}

// CloseWorkspaceAttachment releases this shell only, including after switching.
func (m *App) CloseWorkspaceAttachment() error {
	m.cancelRemoteWorkspaceSwitch(false)
	if connection, ok := m.ws.(shellport.Connection); ok {
		return connection.Close()
	}
	return nil
}
