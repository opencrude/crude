package viewmodel

import (
	"crude/workspace"
)

// ActivateSessionByAgent selects a view owner only in this shell. Resource
// creation, execution and persistence never consult this selection.
func (m *App) ActivateSessionByAgent(agentID string) {
	if m.Workspace == nil {
		return
	}
	if owner := m.SessionForAgent(agentID); owner != nil {
		m.ActiveSession = owner
		if owner.Settings != nil {
			m.ModelName = owner.Settings.Effective.Model
			m.LastSettings = owner.Settings.Effective
		}
	}
}

func (m *App) RefStore() *workspace.ReferenceStore {
	if m.ActiveSession == nil {
		return nil
	}
	return m.ActiveSession.RefStore
}

func (m *App) ActiveSessionConnectionIssue() error {
	if m.Workspace == nil || m.ActiveSession == nil {
		return nil
	}
	return m.SessionConnectionIssue(m.ActiveSession.SessionID)
}

type selectedSessionSettingsResult struct {
	workspace  *workspace.Workspace
	generation uint64
	owner      *workspace.Session
	result     workspace.SessionSettingsResult
}

func (m *App) applySelectedSessionSettings(result selectedSessionSettingsResult) {
	if result.workspace != m.Workspace || result.generation != m.workspaceSwitchGen.Load() ||
		result.owner == nil || m.SessionsByID[result.owner.SessionID] != result.owner {
		return
	}
	if result.result.Err != nil {
		m.Feedback.NotifyError(result.result.Err.Error())
		return
	}
	if !m.ApplySessionSettingsResult(result.result) {
		return
	}
	m.refreshSessionSettingsProjection(result.owner)
	if m.ActiveSession == result.owner {
		m.ModelName = result.result.Snapshot.Effective.Model
		m.LastSettings = result.result.Snapshot.Effective
	}
	m.Feedback.NotifyInfo("settings saved")
}
