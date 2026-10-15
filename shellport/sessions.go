package shellport

import (
	"crude/session"
	"crude/sessiondb"
	"crude/types"
	"crude/workspace"
)

// Sessions is the session registry, lifecycle and per-session settings surface.
type Sessions interface {
	// Sessions is the canonical list of frontend sessions.
	Sessions() []*workspace.Session
	// SessionsByID indexes sessions by session ID.
	SessionsByID() map[string]*workspace.Session
	// SessionsByAgent maps each live orchestrator to its session.
	SessionsByAgent() map[string]*workspace.Session
	// SessionForAgent returns the session that owns the exact agent or its root.
	SessionForAgent(agentID string) *workspace.Session
	// ListSessions returns session metadata for this workspace only.
	ListSessions() []sessiondb.SessionMeta
	// NewAgentSession creates a session and registers its runtime without
	// selecting it in any shell. Callers must serialize workspace lifecycle
	// operations.
	NewAgentSession() *workspace.NewSessionResult
	// OpenAgentSession opens an exact saved session or returns its existing
	// runtime. Opening never selects a client view or starts a provider turn.
	OpenAgentSession(sessionID string) *workspace.NewSessionResult
	// BackendSession returns the underlying backend for the specified session.
	BackendSession(sessionID string) *session.Session
	// SessionConnectionIssue leaves a resumed transcript readable while the UI
	// offers explicit repair of a missing or removed connection.
	SessionConnectionIssue(sessionID string) error
	PrepareMultiAgentToggle(sessionID string) (*workspace.SessionSettingsOperation, error)
	// PrepareSessionCapabilityToggle turns one installed skill/MCP server on or
	// off for the session and every agent it runs.
	PrepareSessionCapabilityToggle(sessionID, id string, enabled bool) (*workspace.SessionSettingsOperation, error)
	PrepareSessionSettingsSave(sessionID string, settings types.Settings) (*workspace.SessionSettingsOperation, error)
	// ApplySessionSettingsResult runs only on the workspace owner, after its
	// caller checks the attachment generation. Replacement owners and old
	// revisions are rejected; the background operation never writes this
	// projection.
	ApplySessionSettingsResult(result workspace.SessionSettingsResult) bool
	// ResetSessionSettingsForSession clears the persisted overrides owned by
	// a specific frontend session without depending on which tab is currently
	// active.
	ResetSessionSettingsForSession(sessionID string) error
}
