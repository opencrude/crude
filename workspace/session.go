package workspace

import (
	"database/sql"
	"maps"
	"slices"
	"sync"
	"time"

	"crude/session"
	"crude/sessiondb"
	"crude/types"
	captypes "crude/types/capability"
)

// Session pairs one workspace conversation with its backend execution owner and
// its own session database. Workspace creation/opening registers it independently
// of any shell selection. Children share the root's reference/settings family.
//
// The shell owns the composer editor, focus, dialogs and rendering projections;
// the backend owns provider context, tools, permissions and durable publication.
// This object retains neutral reference, settings, usage and hydration data.
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
	Settings         *SessionSettingsState
	settingsRevision uint64

	// CreatedAt is when this session was created (for display).
	CreatedAt time.Time

	// usageRecords is the frontend's request-keyed immutable projection. The
	// backend session owns durable writes to the same ledger.
	usageRecords map[string]sessiondb.UsageRecord
	usageMu      sync.RWMutex
	usageVersion uint64
	usageCache   map[usageCacheKey]types.UsageSummary

	// ── Backend session (owned by this frontend session)
	//
	// The backend session (session.Session) is the heavy lifter — it owns
	// the provider connection, tool executor, permission store, and message
	// persistence. Each frontend Session is paired 1:1 with a backend session.

	// backend is the backend session for this frontend session. Created
	// by Workspace.Bootstrap(). Nil until bootstrapped.
	backend *session.Session

	// hydrated fences cold installation into the canonical conversation. Every
	// observer subsequently reads the same retained state, never a one-shot tail.
	hydrated bool
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
		SessionID:    sessionID,
		SessionDir:   sessionDir,
		DB:           db,
		RefStore:     NewReferenceStore(sessionID, sessionDir, db),
		CreatedAt:    time.Now(),
		usageRecords: make(map[string]sessiondb.UsageRecord),
		usageCache:   make(map[usageCacheKey]types.UsageSummary),
	}
	_ = lg // logger is used by AgentState, not Session itself
	return s
}

func (s *Session) attachBackend(backend *session.Session) {
	if s == nil {
		return
	}
	s.backend = backend
	if backend == nil {
		return
	}
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

func cloneSettings(src types.Settings) types.Settings {
	dst := src
	dst.Agents.Categories = copyCategoryMap(src.Agents.Categories)
	dst.Agents.AgentProfiles = types.CloneAgentProfiles(src.Agents.AgentProfiles)
	dst.Agents.Profiles = slices.Clone(src.Agents.Profiles)
	for i := range dst.Agents.Profiles {
		dst.Agents.Profiles[i].Categories = copyCategoryMap(src.Agents.Profiles[i].Categories)
	}
	dst.SessionOverrides = copyCategoryMap(src.SessionOverrides)
	dst.Capabilities = captypes.Clone(src.Capabilities)
	dst.Memory.MemoryCategory = cloneCategorySettings(src.Memory.MemoryCategory)
	dst.Safety.CommandRules = slices.Clone(src.Safety.CommandRules)
	dst.LocalHistory.ExtraIgnorePatterns = slices.Clone(src.LocalHistory.ExtraIgnorePatterns)
	dst.LSP = types.CloneLSPSettings(src.LSP)
	if src.SessionMultiAgent != nil {
		value := *src.SessionMultiAgent
		dst.SessionMultiAgent = &value
	}
	return dst
}

func copyCategoryMap(src map[string]types.CategorySettings) map[string]types.CategorySettings {
	if src == nil {
		return nil
	}
	dst := make(map[string]types.CategorySettings, len(src))
	for key, value := range src {
		dst[key] = cloneCategorySettings(value)
	}
	return dst
}

func cloneCategorySettings(value types.CategorySettings) types.CategorySettings {
	value.AllowedTools = slices.Clone(value.AllowedTools)
	value.DeniedTools = slices.Clone(value.DeniedTools)
	value.SkillModes = maps.Clone(value.SkillModes)
	value.ModelModalities = slices.Clone(value.ModelModalities)
	value.Enabled = cloneSettingPointer(value.Enabled)
	value.ModelTemperature = cloneSettingPointer(value.ModelTemperature)
	value.ModelTopP = cloneSettingPointer(value.ModelTopP)
	value.ContextCompactionPercent = cloneSettingPointer(value.ContextCompactionPercent)
	return value
}

func cloneSettingPointer[T any](source *T) *T {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}
