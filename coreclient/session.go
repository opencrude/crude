package coreclient

import (
	"crude/coreprotocol"
	"crude/session"
	"crude/types"
	"crude/workspace"
)

// installSession refreshes a replica session from a core snapshot. The replica
// has no backend or database; children share their root's settings and
// reference family.
func (v *WorkspaceView) installSession(s *workspace.Session, snapshot coreprotocol.SessionSnapshot, base types.Settings) {
	s.AgentID, s.CreatedAt = snapshot.AgentID, snapshot.CreatedAt
	effective := base
	settings := snapshot.Settings
	effective.SessionMainAgent, effective.Model = settings.MainAgent, settings.Model
	effective.ProviderID, effective.AccountID = settings.ProviderID, settings.AccountID
	effective.ThinkingEffort = settings.ThinkingEffort
	if settings.MultiAgent != nil {
		value := *settings.MultiAgent
		effective.SessionMultiAgent = &value
	}
	if snapshot.Full != nil {
		s.InstallSettingsSnapshot(SettingsSnapshot(*snapshot.Full))
	} else {
		state := workspace.NewSessionSettingsState(effective, base, nil)
		if s.Settings == nil {
			s.Settings = state
		} else {
			*s.Settings = *state
		}
	}
	refs, _ := s.RefStore.(*References)
	if refs == nil {
		refs = newSessionReferences(v, s.SessionID)
		s.RefStore = refs
	}
	refs.bind(v)
	refs.install(snapshot.References)
}

// SettingsSnapshot converts a redacted settings set into the domain form a
// replica session installs. Credentials are absent by construction.
func SettingsSnapshot(result coreprotocol.SessionSettingsResult) session.SettingsSnapshotSet {
	return session.SettingsSnapshotSet{Revision: result.Revision, Effective: SessionSettingsValue(result.Effective),
		Base: SessionSettingsValue(result.Base), Overrides: result.Overrides}
}

// SessionSettingsValue restores the fields Settings JSON deliberately omits.
func SessionSettingsValue(dto coreprotocol.SessionSettingsDTO) types.Settings {
	value := dto.Value
	value.SessionMainAgent, value.SessionOverrides, value.SessionMultiAgent = dto.MainAgent, dto.Overrides, dto.MultiAgent
	value.SessionDisabledCapabilities = dto.DisabledCapabilities
	return value
}
