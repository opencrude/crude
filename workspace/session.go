package workspace

import (
	"database/sql"
	"time"

	"crude/session"
	"crude/sessiondb"
	"crude/types"
)

// Session is the frontend state for one agent conversation. It is the TUI
// counterpart to the backend session.Session (in crude/session/) — the
// backend runs the agent loop, the LLM calls, the tool execution; the
// frontend Session owns conversation data, the chat input draft, the
// per-agent dialogs (confirm, question, yolo, text overlay, composer),
// the reference store, and the cost/token tracking.
//
// One Session per orchestrator. Sub-agents do NOT get their own Session —
// they get a SubPane (owned by PaneMgr) with conversation data and a temporary
// MessageList projection, and share the orchestrator's Session for dialogs and
// references.
//
// The Session is created when an orchestrator is added (via
// AgentManager.AddOrchestrator) and destroyed when the orchestrator is
// removed (via AgentManager.Remove). The AgentNode.State field points to
// the Session's AgentState (streaming, thinking, conversation data, etc.).
//
// Persistence: each Session owns a sessionID, sessionDir, and *sql.DB.
// These are the SQLite handles for this conversation's messages, cost
// snapshots, sub-agent results, and prompt references. In the current
// architecture, all sessions share the same .db file (the app creates
// one session.Session). In a future multi-session architecture, each
// Session would have its own .db file.
type Session struct {
	// SessionID is the unique identifier for this session's backend
	// (session.Session). Used for SQLite persistence and session resume.
	SessionID string

	// AgentID is the orchestrator agent ID for this session. Empty until
	// an orchestrator is started for the session.
	AgentID string

	// SessionDir is the filesystem directory for this session's logs,
	// reports, and cost snapshots. Typically ~/.crude/sessions/<id>/.
	SessionDir string

	// DB is the SQLite handle for this session's persistence. May be nil
	// if persistence is disabled.
	DB *sql.DB

	// RefStore manages @references (pasted text, images, selected files)
	// for this session's chat input. Each session has its own reference
	// store because references are tied to a specific conversation.
	RefStore *ReferenceStore

	// AgentState holds the streaming/thinking state and live conversation data
	// for this session's orchestrator. This is what AgentNode.State points to.
	AgentState *AgentState

	// Settings is the domain snapshot for this conversation's provider/model/
	// thinking overrides. The TUI creates a retained presentation projection
	// from this data; workspace never owns the settings form or dialog widgets.
	Settings *SessionSettingsState

	// ── Per-session cost/token tracking ─
	//
	// These are updated by agent events (usage, done) and displayed in
	// the status bar. They are per-session because each agent conversation
	// has its own token usage.

	// CreatedAt is when this session was created (for display).
	CreatedAt time.Time

	// TokenPrompt, TokenCompletion, TokenCached track the last LLM call's
	// token counts for this session. Displayed in the status bar.
	TokenPrompt     int
	TokenCompletion int
	TokenCached     int

	// SessionCost is the cumulative cost (USD) for this session.
	SessionCost float64

	// ContextWindow, ContextUsed track the context window usage for the
	// last LLM call in this session.
	ContextWindow int
	ContextUsed   int

	// TokenEstimated is true when the token count is an estimate.
	TokenEstimated bool
	// CostUpperBound is true when the provider reports total prompt tokens but
	// not cache-read tokens, so the accumulated cost assumes no cache discount.
	CostUpperBound bool

	// ── Backend session (owned by this frontend session)
	//
	// The backend session (session.Session) is the heavy lifter — it owns
	// the provider connection, tool executor, permission store, and message
	// persistence. Each frontend Session is paired 1:1 with a backend session.

	// backend is the backend session for this frontend session. Created
	// by Workspace.Bootstrap(). Nil until bootstrapped.
	backend *session.Session

	// hydratedMessages stores converted messages from session resume.
	// Set by Workspace.Hydrate(), consumed by the TUI layer on startup.
	hydratedMessages []ChatMessageView

	// hydratedRefs stores prompt references from session resume.
	hydratedRefs []sessiondb.Reference
}

// NewSession creates a frontend session with the given persistence handles.
// Presentation widgets are created by the TUI projection layer. The reference
// store is created from the session's DB.
//
// Parameters:
// - sessionID, sessionDir, db: persistence handles for this session.
// - lg: the logger (shared, not owned).
func NewSession(sessionID, sessionDir string, db *sql.DB, lg Logger) *Session {
	s := &Session{
		SessionID:  sessionID,
		SessionDir: sessionDir,
		DB:         db,
		RefStore:   NewReferenceStore(sessionID, sessionDir, db),
		CreatedAt:  time.Now(),
	}
	_ = lg // logger is used by AgentState, not Session itself
	return s
}

// SessionSettingsState is presentation-neutral settings state for one
// conversation. Effective is the result of applying Overrides to Base.
type SessionSettingsState struct {
	Effective types.Settings
	Base      types.Settings
	Overrides map[string]types.CategorySettings
}

func NewSessionSettingsState(effective, base types.Settings, overrides map[string]types.CategorySettings) *SessionSettingsState {
	return &SessionSettingsState{
		Effective: cloneSettings(effective),
		Base:      cloneSettings(base),
		Overrides: copyCategoryMap(overrides),
	}
}

func (s *SessionSettingsState) SetInheritance(effective, base types.Settings, overrides map[string]types.CategorySettings) {
	if s == nil {
		return
	}
	s.Effective = cloneSettings(effective)
	s.Base = cloneSettings(base)
	s.Overrides = copyCategoryMap(overrides)
}

func (s *SessionSettingsState) ResetToWorkspace(base types.Settings) {
	if s == nil {
		return
	}
	s.Base = cloneSettings(base)
	s.Effective = cloneSettings(base)
	s.Overrides = make(map[string]types.CategorySettings)
}

func cloneSettings(src types.Settings) types.Settings {
	dst := src
	dst.Agents.Categories = copyCategoryMap(src.Agents.Categories)
	dst.SessionOverrides = copyCategoryMap(src.SessionOverrides)
	if src.SessionMultiAgent != nil {
		value := *src.SessionMultiAgent
		dst.SessionMultiAgent = &value
	}
	return dst
}

func copyCategoryMap(src map[string]types.CategorySettings) map[string]types.CategorySettings {
	dst := make(map[string]types.CategorySettings, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// TakeHydratedMessages returns and clears messages loaded during resume.
func (s *Session) TakeHydratedMessages() []ChatMessageView {
	messages := s.hydratedMessages
	s.hydratedMessages = nil
	return messages
}

// TakeHydratedRefs returns and clears references loaded during resume.
func (s *Session) TakeHydratedRefs() []sessiondb.Reference {
	refs := s.hydratedRefs
	s.hydratedRefs = nil
	return refs
}
