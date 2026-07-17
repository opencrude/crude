package workspace

import (
	"database/sql"
	"time"

	"crude/session"
	"crude/sessiondb"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
)

// Session is the frontend state for one agent conversation. It is the TUI
// counterpart to the backend session.Session (in crude/session/) — the
// backend runs the agent loop, the LLM calls, the tool execution; the
// frontend Session owns the message list, the chat input draft, the
// per-agent dialogs (confirm, question, yolo, text overlay, composer),
// the reference store, and the cost/token tracking.
//
// One Session per orchestrator. Sub-agents do NOT get their own Session —
// they get a SubPane (owned by PaneMgr) which has its own MessageList but
// shares the orchestrator's Session for dialogs and references.
//
// The Session is created when an orchestrator is added (via
// AgentManager.AddOrchestrator) and destroyed when the orchestrator is
// removed (via AgentManager.Remove). The AgentNode.State field points to
// the Session's AgentState (streaming, thinking, message list, etc.).
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

	// AgentState holds the streaming/thinking state and the message list
	// for this session's orchestrator. This is what AgentNode.State points to.
	AgentState *AgentState

	// Per-session dialogs ─
	//
	// These are the tool-confirmation and user-interaction dialogs. Each
	// is a singleton within the session — only one can be active at a time.
	// The overlay mode (OverlayState.Mode) determines which is rendered.

	// Confirm is the tool-confirmation dialog ("Allow write to foo.go?").
	Confirm widgets.ConfirmDialog

	// QuestionForm is the multi-question form (when the agent asks the
	// user questions via the "question" tool).
	QuestionForm widgets.QuestionForm

	// YoloDlg is the YOLO confirmation dialog (when the user toggles
	// auto-approve mode).
	YoloDlg widgets.YoloDialog

	// TextOverlay is the file-edit overlay (when the agent proposes an
	// edit and the user reviews it in a full-screen overlay).
	TextOverlay widgets.TextOverlay

	// Composer is the multi-line composer popup (for drafting long messages).
	Composer *widgets.Composer

	// Settings is this conversation's provider/model/thinking override editor.
	// It is intentionally per session so tabs never leak overrides.
	Settings *widgets.SessionSettings

	// ── Per-session callbacks ──
	//
	// These bridge the TUI to the backend for session-specific actions.

	// OnConfirmResult is called when the user responds to a tool confirmation.
	OnConfirmResult func(ttypes.ConfirmResult)

	// OnQuestionResult is called when the user submits answers to a question form.
	OnQuestionResult func(ttypes.QuestionResult)

	// OnCancelSubAgent is called when the user requests cancellation of a
	// specific sub-agent.
	OnCancelSubAgent func(agentID string)

	// OnOrchestratorReset is called when the user resets this orchestrator's
	// per-session config overrides.
	OnOrchestratorReset func(orchID string)

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

	// theme is a reference to the workspace's theme (for creating widgets).
	theme *theme.Theme

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
	hydratedMessages []ttypes.ChatMessage

	// hydratedRefs stores prompt references from session resume.
	hydratedRefs []sessiondb.Reference
}

// NewSession creates a frontend session with the given persistence handles.
// The dialogs (Confirm, QuestionForm, etc.) are created with the workspace's
// theme. The reference store is created from the session's DB.
//
// Parameters:
// - th: the workspace's theme (shared, not owned).
// - sessionID, sessionDir, db: persistence handles for this session.
// - lg: the logger (shared, not owned).
func NewSession(th *theme.Theme, sessionID, sessionDir string, db *sql.DB, lg Logger) *Session {
	s := &Session{
		SessionID:    sessionID,
		SessionDir:   sessionDir,
		DB:           db,
		RefStore:     NewReferenceStore(sessionID, sessionDir, db),
		Confirm:      widgets.NewConfirmDialog(th),
		QuestionForm: widgets.NewQuestionForm(th),
		YoloDlg:      widgets.NewYoloDialog(th),
		TextOverlay:  widgets.NewTextOverlay(th),
		Composer:     widgets.NewComposer(th),
		CreatedAt:    time.Now(),
		theme:        th,
	}
	_ = lg // logger is used by AgentState, not Session itself
	return s
}

// NewSessionWithAgentState creates a session AND its AgentState (with a
// fresh message list). Used when an orchestrator is added — the AgentState
// is what gets stored in AgentNode.State.
func NewSessionWithAgentState(th *theme.Theme, sessionID, sessionDir string, db *sql.DB, lg Logger, modelName, username string) *Session {
	s := NewSession(th, sessionID, sessionDir, db, lg)
	ml := widgets.NewMessageList(th)
	ml.SetModelName(modelName)
	ml.SetUsername(username)
	s.AgentState = NewAgentState(sessionID, ml, lg)
	return s
}

// HydratedMessages returns the messages loaded from a resumed session's DB.
// Consumed by the TUI layer on startup.
func (s *Session) HydratedMessages() []ttypes.ChatMessage {
	return s.hydratedMessages
}

// HydratedRefs returns the prompt references loaded from a resumed session's DB.
// Consumed by the TUI layer on startup.
func (s *Session) HydratedRefs() []sessiondb.Reference {
	return s.hydratedRefs
}
