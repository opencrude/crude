package coreclient

import (
	"time"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

// Session contains only client-owned read data. Children share their root's
// settings and reference family; there is no backend, database or runtime here.
type Session struct {
	SessionID, AgentID string
	CreatedAt          time.Time
	AgentState         *workspace.AgentState
	Settings           *workspace.SessionSettingsState
	RefStore           *References
}

func (s *Session) install(snapshot coreprotocol.SessionSnapshot, base types.Settings) {
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
	state := workspace.NewSessionSettingsState(effective, base, nil)
	if s.Settings == nil {
		s.Settings = state
	} else {
		*s.Settings = *state
	}
	if s.RefStore == nil {
		s.RefStore = NewReferences()
	}
	s.RefStore.install(snapshot.References)
}
