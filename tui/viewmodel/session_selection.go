package viewmodel

import (
	"crude/shellport"
	"crude/tui/ui"
	"crude/workspace"
)

// ActivateSessionByAgent selects a view owner only in this shell. Resource
// creation, execution and persistence never consult this selection.
func (m *App) ActivateSessionByAgent(agentID string) {
	if m.ws == nil {
		return
	}
	if owner := m.ws.SessionForAgent(agentID); owner != nil {
		if owner != m.ActiveSession {
			if screen := m.sessionTextOverlay(); screen != nil && screen.RefID() == "session-info" {
				m.overlayHost.Close(ui.OverlayTextOverlay)
			}
		}
		if m.referenceOverlay != nil && (owner != m.referenceOverlay.session || agentID != m.referenceOverlay.agentID) {
			m.overlayHost.Close(ui.OverlayTextOverlay)
		}
		if m.attachmentOpen != nil && (owner != m.attachmentOpen.session || agentID != m.attachmentOpen.agentID) {
			m.cancelAttachmentOpen()
		}
		m.ActiveSession = owner
		if owner.Settings != nil {
			m.ModelName = owner.Settings.Effective.Model
			m.LastSettings = owner.Settings.Effective
		}
	}
}

func (m *App) RefStore() workspace.SessionReferences {
	if m.ActiveSession == nil {
		return nil
	}
	return m.ActiveSession.RefStore
}

func (m *App) ActiveSessionConnectionIssue() error {
	if m.ws == nil || m.ActiveSession == nil {
		return nil
	}
	return m.ws.SessionConnectionIssue(m.ActiveSession.SessionID)
}

type selectedSessionSettingsResult struct {
	workspace  shellport.Workspace
	generation uint64
	owner      *workspace.Session
	result     workspace.SessionSettingsResult
	label      string
}

func (m *App) applySelectedSessionSettings(result selectedSessionSettingsResult) {
	if result.workspace != m.ws || result.generation != m.workspaceSwitchGen.Load() ||
		result.owner == nil || m.ws.SessionsByID()[result.owner.SessionID] != result.owner {
		return
	}
	if result.result.Err != nil {
		m.Feedback.NotifyError(result.result.Err.Error())
		return
	}
	if !m.ws.ApplySessionSettingsResult(result.result) {
		return
	}
	m.refreshSessionSettingsProjection(result.owner)
	if m.ActiveSession == result.owner {
		m.ModelName = result.result.Snapshot.Effective.Model
		m.LastSettings = result.result.Snapshot.Effective
	} else {
		// A completed save belongs to its captured session, not the tab that
		// happened to become active while the operation was in flight.
		return
	}
	label := result.label
	if label == "" {
		label = "settings saved"
	}
	m.Feedback.NotifyInfo(label)
}
