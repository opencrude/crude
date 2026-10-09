package shellport

import (
	"time"

	"crude/session"
	"crude/sessiondb"
	"crude/types"
	"crude/workspace"
)

// Runtime is the identity, lifecycle and startup surface of one project workspace.
type Runtime interface {
	// RootPath is the absolute project directory the workspace is rooted at.
	RootPath() string
	// Username is the resolved username used for message attribution.
	Username() string
	// StartTime is when the workspace was created.
	StartTime() time.Time
	// InitialSettings is the neutral startup settings snapshot.
	InitialSettings() types.Settings
	// InitialModelName is startup display metadata, not a selected session.
	InitialModelName() string
	// Logger is the shared logger (already a narrow interface).
	Logger() workspace.Logger
	// WorkspaceDB is the workspace metadata database; nil when unavailable.
	WorkspaceDB() WorkspaceStore
	// SetEventBus connects workspace services to the workspace-to-frontend event
	// path.
	SetEventBus(bus *workspace.EventBus)
	// SwitchProject re-roots this workspace at a new project directory.
	// Closes open sessions and recreates every project-bound domain manager. Keeps
	// sessionCfg, homeDir, sender, application defaults, and frontend connections
	// so the TUI can continue without restarting.
	SwitchProject(projectDir string) error
	// TakeStartupIssues transfers pending presentation-neutral diagnostics once.
	TakeStartupIssues() []types.OperationIssue
	// LastRegistryError returns the most recent non-fatal session-registry write
	// failure, or nil. Callers may surface it in the status bar; it must not abort
	// session creation or agent teardown.
	LastRegistryError() error
	// SetEventSender stores the EventSender (TUI) for orchestrator creation.
	// Called by app.Run() after creating the TUI.
	SetEventSender(sender session.EventSender)
}

// WorkspaceStore is the workspace-level metadata database, reduced to what the shell reads.
type WorkspaceStore interface {
	// ListSessions returns available sessions registered in this workspace,
	// most recent first. Rows marked db_missing are excluded (see
	// MarkSessionDBMissing).
	ListSessions() ([]sessiondb.SessionMeta, error)
}
