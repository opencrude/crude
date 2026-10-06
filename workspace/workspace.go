package workspace

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"crude/agent"
	"crude/capability"
	"crude/collaboration"
	"crude/localhistory"
	"crude/session"
	ttypes "crude/types"
)

// NewSessionResult identifies the orchestrator created by a launcher action.
// Turn input, blocking responses, human steering, and cancellation are routed through
// generation-bound Workspace methods rather than cross-layer channels.
type NewSessionResult struct {
	OrchID       string
	Error        string
	NeedsAccount bool
}

// Workspace is the project-scoped composition root — the IDE instance.
//
// One Workspace per project directory, inspired by JetBrains' project model.
// Each workspace gets:
// - A dedicated folder under ~/.crude/workspaces/<hash>/ (hash of project dir)
// - Its own SQLite database for workspace-level metadata
// - Its own file tree, LSP connections, editor tabs, agent sessions
//
// The workspace owns ALL business state for one IDE instance:
// - AI agent sessions and orchestrators
// - Editor buffers and tab state
// - LSP server connections and diagnostics cache
// - File tree model
// - Workspace-specific settings (inheriting from app config)
//
// The App struct (in tui/viewmodel) embeds *Workspace and delegates to it.
// Multiple workspaces can coexist for multi-project support.
//
// Session-scoped state (chat input, per-agent dialogs, message lists, cost
// tracking) lives on Session, one per agent conversation. The Workspace
// holds the agent tree (AgentManager) which links to Sessions via AgentNode.State.
type Workspace struct {
	// eventBus publishes workspace-originated events to the active frontend.
	eventBus *EventBus

	// RootPath is the absolute path to the project directory this workspace
	// is rooted at. The file tree, LSP manager, and file watcher all key
	// off this path.
	RootPath string

	// Workspace-level persistence            ─
	//
	// Each workspace has its own SQLite database for project-scoped
	// metadata that persists across sessions: workspace settings,
	// session registry, usage statistics. This is separate from
	// per-conversation session databases.

	// WorkspaceDB is the workspace-level database. Stores workspace
	// settings, session registry, and project metadata. Created in
	// NewWorkspace from the project directory's hash-based path.
	WorkspaceDB *WorkspaceDB

	// WorkspaceDir is the dedicated folder for this workspace.
	// Layout: ~/.crude/workspaces/<hash>/
	WorkspaceDir string

	// LocalHistory owns immutable project snapshots and restore plans. Capture
	// scheduling remains opt-in until the watcher/settings phase is enabled.
	LocalHistory    *localhistory.Store
	HistoryService  *LocalHistoryService
	localHistoryErr error

	// ── Managers (workspace-scoped) ──
	//
	// Each manager is the single source of truth for its concern. All are
	// created in NewWorkspace and live for the workspace's lifetime.

	// AgentMgr owns the agent tree (orchestrator + sub-agents). Each
	// AgentNode.State is a *Session (frontend session for one agent).
	AgentMgr *AgentManager

	// PaneMgr owns the sub-pane display state (thinking panes, agent panes).
	// Sub-panes are rendered inline in the parent agent's message list.
	PaneMgr *PaneManager

	// LSPMgr owns all LSP server connections for this workspace's root
	// directory. One LSPManager per workspace (rooted at RootPath).
	LSPMgr *LSPManager

	// ProjectSearch owns Find in Files execution identity and cancellation.
	// The TUI owns query/result presentation only.
	ProjectSearch *ProjectSearchService

	// FileOps owns all filesystem mutations for this workspace.
	// Every file creation, deletion, rename, and LSP workspace edit
	// goes through this service. Callers own the returned operation results.
	FileOps *FileOpsService

	// Git owns source-control reads and mutations for this workspace.
	// Frontends consume neutral snapshots and never execute Git directly.
	Git              *GitService
	Capabilities     *capability.Service
	Collaboration    *collaboration.Store
	FileOwnership    *agent.FileOwnership
	capabilityMu     sync.RWMutex
	collaborationErr error
	instanceID       string
	// Documents is the single scheduler and disk-commit authority for editor
	// buffers in this workspace.
	Documents *DocumentSaveService

	// TermMgr owns in-app PTY sessions (the terminal multiplexer).
	TermMgr *TerminalManager
	// Status retains typed workspace-scoped lifecycle, activity, issue, and
	// notification projections. Resource managers remain authoritative.
	Status             *StatusCenter
	registryCostWriter *registryCostWriter

	// FocusStack manages keyboard focus as a stack. Pushing a floating
	// window or clicking the tree pushes focus; closing pops back.
	FocusStack *FocusStack

	// ── Sessions
	//
	// The workspace owns all AI conversations for this project. Sessions
	// is the canonical list; SessionsByID indexes by session ID for fast
	// lookup. ActiveSession points to the conversation currently displayed
	// in the chat view. nil when the launcher/workspace selector is showing.

	// Sessions is the canonical list of all frontend sessions in this workspace.
	Sessions []*Session

	// SessionsByID indexes sessions by their session ID.
	SessionsByID map[string]*Session

	// SessionsByAgent maps every live orchestrator to its frontend session.
	// A session may own more than one orchestrator over its lifetime.
	SessionsByAgent map[string]*Session

	// ActiveSession is the currently-active frontend session.
	ActiveSession *Session

	// ── Backend session config (set by app layer)
	//
	// These carry the backend knowledge needed to create/resume sessions
	// without callback indirection. Set once during app.Run().

	// sessionCfg is the session config derived from app config.
	sessionCfg *session.Config

	// appDefaults is the application-layer settings snapshot supplied by
	// app/ from ~/.crude/config.json. Workspace overrides layer on top.
	appDefaults ttypes.AppSettings

	// homeDir is the user's home directory.
	homeDir string

	// sender is the EventSender (the TUI) for starting orchestrators.
	// Set after TUI creation.
	sender session.EventSender

	// OnAppSettingsSave persists application-layer settings to config.json.
	// Workspace overrides are stored separately in workspace.db.
	OnAppSettingsSave func(s ttypes.AppSettings) (ttypes.AppSettings, error)
	// OnAppAccountChange persists one application account without writing other
	// settings drafts. The app layer owns the config file and account manager.
	OnAppAccountChange func(change ttypes.AccountChange) error

	// ── Shared state (references, not owned)
	//
	// These are references to objects shared with the TUI layer.

	// Logger is the global logger.
	Logger Logger

	// ModelName is the display name of the active model.
	// Set during workspace creation, updated by the TUI layer.
	ModelName string

	// InitialSettings is the neutral settings snapshot supplied by the app
	// layer. The TUI may use it to initialize its presentation state.
	InitialSettings ttypes.Settings

	// Username is the resolved username (for message attribution).
	Username string

	// StartTime is when this workspace was created.
	StartTime time.Time

	// registryErr holds the most recent non-fatal workspace_sessions
	// registry write failure (RegisterSession / SyncSessionMeta / CloseSession).
	// Cleared on the next successful registry write. Session create/resume
	// must not fail because of this — it is surfaced for logging/UI only.
	registryErr    error
	persistenceMu  sync.Mutex
	workspaceDBErr error
	startupIssues  []ttypes.OperationIssue
}

// SetEventBus connects workspace services to the workspace-to-frontend event path.
func (w *Workspace) SetEventBus(bus *EventBus) {
	w.eventBus = bus
	if w.FileOps != nil {
		w.FileOps.eventBus = bus
	}
	if w.TermMgr != nil {
		w.TermMgr.SetEventBus(bus)
	}
	if w.HistoryService != nil {
		w.HistoryService.SetEventBus(bus)
	}
	if w.Documents != nil {
		w.Documents.SetEventBus(bus)
	}
}

// NewWorkspace creates an empty workspace rooted at the given directory.
//
// Parameters:
//   - rootPath: the project directory (file tree root, LSP root). If empty,
//     the current working directory is used.
//   - modelName, username: display metadata.
//   - initial: the initial settings snapshot.
//   - lg: the logger.
//
// Sessions are created separately via Bootstrap() / CreateSession() /
// ResumeSession(). This constructor only prepares the workspace shell.
func NewWorkspace(
	rootPath string,
	modelName string,
	username string,
	initial ttypes.Settings,
	lg Logger,
) *Workspace {
	if rootPath == "" {
		rootPath, _ = os.Getwd()
	}

	lspMgr := NewLSPManager(lg)

	// Initialize workspace-level persistence. Each workspace gets a
	// dedicated folder and database based on the project directory hash.
	wsDir, wsErr := WorkspaceDir(rootPath)
	if wsErr != nil {
		lg.Warn("WORKSPACE", "dir_create_failed", "error", wsErr.Error())
	}
	wsDB, dbErr := OpenWorkspaceDB(rootPath)
	if dbErr != nil {
		lg.Warn("WORKSPACE", "db_open_failed", "error", dbErr.Error())
	}
	historyStore, historyErr := openLocalHistory(rootPath, wsDir)
	if historyErr != nil {
		lg.Warn("WORKSPACE", "local_history_open_failed", "error", historyErr.Error())
	}

	instanceID := collaboration.NewInstanceID()
	var collaborationStore *collaboration.Store
	var collaborationErr error
	if wsDB != nil {
		collaborationStore, collaborationErr = collaboration.Attach(
			wsDB.DB, canonicalizePath(rootPath), filepath.Join(wsDir, "collaboration-locks"),
		)
		if collaborationErr != nil {
			lg.Warn("WORKSPACE", "collaboration_open_failed", "error", collaborationErr.Error())
		}
	}

	ws := &Workspace{
		RootPath:           rootPath,
		WorkspaceDir:       wsDir,
		WorkspaceDB:        wsDB,
		LocalHistory:       historyStore,
		localHistoryErr:    historyErr,
		AgentMgr:           NewAgentManager(),
		PaneMgr:            NewPaneManager(),
		LSPMgr:             lspMgr,
		ProjectSearch:      NewProjectSearchService(),
		FileOps:            NewFileOpsService(rootPath, nil, lg),
		Git:                NewGitService(rootPath),
		Collaboration:      collaborationStore,
		collaborationErr:   collaborationErr,
		instanceID:         instanceID,
		FileOwnership:      agent.NewFileOwnership(),
		Documents:          NewDocumentSaveService(),
		TermMgr:            NewTerminalManager(rootPath),
		Status:             NewStatusCenter(workspaceSQLDB(wsDB), lg),
		registryCostWriter: newRegistryCostWriter(wsDB, lg),
		FocusStack:         NewFocusStack(),
		Sessions:           make([]*Session, 0),
		SessionsByID:       make(map[string]*Session),
		SessionsByAgent:    make(map[string]*Session),
		Logger:             lg,
		ModelName:          modelName,
		InitialSettings:    initial,
		Username:           username,
		StartTime:          time.Now(),
		workspaceDBErr:     dbErr,
	}
	if historyStore != nil {
		ws.HistoryService = NewLocalHistoryService(rootPath, historyStore, lg)
	}

	// Self-heal the session registry from on-disk session DBs. Runs on
	// workspace open (not the launcher workspace-list path, which is
	// deliberately read-only and time-bounded). Typical session counts are
	// small; each DB is opened read-only and corrupt files are skipped.
	_ = ws.BackfillSessionRegistry()
	ws.queuePersistenceStartupIssues()

	return ws
}

// ensureWorkspacePersistence repairs a transient startup open failure before
// any session can begin. Memory and the session registry are mandatory parts
// of a workspace session; silently launching without them creates a degraded
// runtime that cannot self-heal later.
func (w *Workspace) ensureWorkspacePersistence() error {
	if w == nil {
		return fmt.Errorf("workspace is nil")
	}
	w.persistenceMu.Lock()
	defer w.persistenceMu.Unlock()
	if w.instanceID == "" {
		w.instanceID = collaboration.NewInstanceID()
	}
	if w.WorkspaceDB != nil && w.WorkspaceDB.Memory != nil {
		if w.Collaboration != nil {
			return nil
		}
		store, err := collaboration.Attach(
			w.WorkspaceDB.DB, canonicalizePath(w.RootPath), filepath.Join(w.WorkspaceDir, "collaboration-locks"),
		)
		if err != nil {
			w.collaborationErr = err
			return fmt.Errorf("attach collaboration runtime: %w", err)
		}
		w.Collaboration = store
		w.collaborationErr = nil
		return nil
	}

	previousErr := w.workspaceDBErr
	db, err := OpenWorkspaceDB(w.RootPath)
	if err != nil {
		w.workspaceDBErr = err
		return err
	}
	oldDB := w.WorkspaceDB
	oldStatus := w.Status
	oldWriter := w.registryCostWriter
	w.WorkspaceDB = db
	w.workspaceDBErr = nil
	collaborationStore, collaborationErr := collaboration.Attach(
		db.DB, canonicalizePath(w.RootPath), filepath.Join(w.WorkspaceDir, "collaboration-locks"),
	)
	if collaborationErr != nil {
		_ = db.Close()
		w.WorkspaceDB = oldDB
		w.workspaceDBErr = previousErr
		return fmt.Errorf("restore collaboration runtime: %w", collaborationErr)
	}
	w.Collaboration = collaborationStore
	w.collaborationErr = nil
	for _, frontendSession := range w.Sessions {
		if frontendSession != nil && frontendSession.backend != nil {
			frontendSession.backend.SetMemoryStore(db.Memory)
			frontendSession.backend.StartMemoryMaintenance()
		}
	}
	w.Status = NewStatusCenter(workspaceSQLDB(db), w.Logger)
	w.registryCostWriter = newRegistryCostWriter(db, w.Logger)
	if oldWriter != nil {
		oldWriter.Close()
	}
	if oldStatus != nil {
		oldStatus.Close()
	}
	if oldDB != nil {
		_ = oldDB.Close()
	}
	if w.Logger != nil {
		w.Logger.Info("WORKSPACE", "persistence_recovered", "had_startup_error", previousErr != nil)
	}
	status := w.Status
	bus := w.eventBus
	go func() {
		if err := status.Hydrate(); err != nil {
			if w.Logger != nil {
				w.Logger.Warn("STATUS", "notification_hydrate_failed", "error", err.Error())
			}
			return
		}
		if bus != nil {
			bus.Send(StatusCenterHydratedMsg{})
		}
	}()
	return nil
}

// SessionDir returns the session directory for the active session.
// Convenience accessor — delegates to ActiveSession.
func (w *Workspace) SessionDir() string {
	if w.ActiveSession != nil {
		return w.ActiveSession.SessionDir
	}
	return ""
}

// SessionDB returns the SQLite handle for the active session.
// Convenience accessor — delegates to ActiveSession.
func (w *Workspace) SessionDB() *sql.DB {
	if w.ActiveSession != nil {
		return w.ActiveSession.DB
	}
	return nil
}

// RefStore returns the reference store for the active session.
// Convenience accessor — delegates to ActiveSession.
func (w *Workspace) RefStore() *ReferenceStore {
	if w.ActiveSession != nil {
		return w.ActiveSession.RefStore
	}
	return nil
}

// WorkspaceRoot returns the project root path (alias for RootPath).
// Provided as a method for interface compatibility.
func (w *Workspace) WorkspaceRoot() string {
	return w.RootPath
}

// Close shuts down the workspace and releases all resources.
// This includes closing the workspace database and stopping LSP connections.
// Called when the IDE instance is shutting down.
func (w *Workspace) Close() {
	w.capabilityMu.Lock()
	defer w.capabilityMu.Unlock()
	w.CloseAllSessions()
	w.CancelProjectSearch()
	if w.Documents != nil {
		w.Documents.Close()
	}
	if w.Capabilities != nil {
		w.Capabilities.Close()
	}
	if w.TermMgr != nil {
		w.TermMgr.CloseAll()
	}
	if w.LSPMgr != nil {
		w.LSPMgr.Stop()
	}
	if w.Status != nil {
		w.Status.Close()
		w.Status = nil
	}
	if w.registryCostWriter != nil {
		w.registryCostWriter.Close()
		w.registryCostWriter = nil
	}
	if w.Collaboration != nil {
		_ = w.Collaboration.Close()
		w.Collaboration = nil
	}
	if w.WorkspaceDB != nil {
		_ = w.WorkspaceDB.Close()
		w.WorkspaceDB = nil
	}
	if w.HistoryService != nil {
		w.HistoryService.Stop()
		w.HistoryService = nil
	}
	if w.LocalHistory != nil {
		_ = w.LocalHistory.Close()
		w.LocalHistory = nil
	}
}

// CloseAllSessions closes every open backend session.
func (w *Workspace) CloseAllSessions() {
	for _, fs := range w.Sessions {
		if fs != nil && fs.backend != nil {
			fs.backend.Close()
		}
		if fs != nil && fs.SessionID != "" {
			w.persistSessionRegistry(fs)
		}
	}
	w.Sessions = w.Sessions[:0]
	w.SessionsByID = make(map[string]*Session)
	w.SessionsByAgent = make(map[string]*Session)
	w.ActiveSession = nil
}

// LastRegistryError returns the most recent non-fatal session-registry write
// failure, or nil. Callers may surface it in the status bar; it must not abort
// session creation or agent teardown.
func (w *Workspace) LastRegistryError() error {
	if w == nil {
		return nil
	}
	return w.registryErr
}

// noteRegistryErr records and logs a non-fatal registry persistence failure.
func (w *Workspace) noteRegistryErr(op string, err error) {
	if err == nil {
		w.registryErr = nil
		return
	}
	w.registryErr = err
	if w.Logger != nil {
		w.Logger.Error("WORKSPACE", op, "error", err.Error())
	}
}

// persistSessionRegistry ensures the session is listed in workspace_sessions
// with up-to-date metadata and a closed_at stamp. Reuses RegisterSession,
// SyncSessionMeta / syncSessionMetaFromDB, and CloseSession. Never deletes the
// session DB. Safe to call when the session was never registered (legacy).
func (w *Workspace) persistSessionRegistry(fs *Session) {
	if w == nil || w.WorkspaceDB == nil || fs == nil || fs.SessionID == "" {
		return
	}
	model, provider := "", ""
	if fs.backend != nil {
		model = fs.backend.ModelName()
		provider = fs.backend.SettingsSnapshot().ProviderID
	}
	projectName := filepath.Base(w.RootPath)
	if err := w.WorkspaceDB.RegisterSession(fs.SessionID, w.RootPath, projectName, model, provider); err != nil {
		w.noteRegistryErr("session_register_failed", err)
		// Still attempt meta sync / close stamp — Register may have failed for
		// an unrelated reason while the row already exists.
	} else {
		w.noteRegistryErr("", nil)
	}
	if fs.DB != nil {
		if err := w.WorkspaceDB.syncSessionMetaFromDB(fs.DB, fs.SessionID); err != nil {
			w.noteRegistryErr("session_sync_failed", err)
			if err := w.WorkspaceDB.SyncSessionMeta(w.RootPath, fs.SessionID); err != nil {
				w.noteRegistryErr("session_sync_failed", err)
			}
		}
	} else if err := w.WorkspaceDB.SyncSessionMeta(w.RootPath, fs.SessionID); err != nil {
		w.noteRegistryErr("session_sync_failed", err)
	}
	if err := w.WorkspaceDB.CloseSession(fs.SessionID); err != nil {
		w.noteRegistryErr("session_close_registry_failed", err)
	}
}

// ── File operation methods ──
//
// These methods delegate to FileOpsService and return command-owned results.
// The TUI calls them instead of performing filesystem mutations directly.

// CreateFile creates an empty file at the given path.
// The path must be absolute and under the workspace root.
// The result is returned directly to the caller.
func (w *Workspace) CreateFile(path string) FileOperationResultMsg {
	if w.FileOps == nil {
		w.FileOps = NewFileOpsService(w.RootPath, w.eventBus, w.Logger)
	}
	return w.FileOps.CreateFile(path)
}

// CreateFolder creates a directory (and parents) at the given path.
// The path must be absolute and under the workspace root.
// The result is returned directly to the caller.
func (w *Workspace) CreateFolder(path string) FileOperationResultMsg {
	if w.FileOps == nil {
		w.FileOps = NewFileOpsService(w.RootPath, w.eventBus, w.Logger)
	}
	return w.FileOps.CreateFolder(path)
}

// RenamePath renames a file or folder from oldPath to newPath.
// Both paths must be absolute and under the workspace root.
// The result is returned directly to the caller.
func (w *Workspace) RenamePath(oldPath, newPath string) FileOperationResultMsg {
	if w.FileOps == nil {
		w.FileOps = NewFileOpsService(w.RootPath, w.eventBus, w.Logger)
	}
	return w.FileOps.RenamePath(oldPath, newPath)
}

// DeletePath removes a file or directory at the given path.
// Directories are removed recursively.
// The path must be absolute and under the workspace root.
// The result is returned directly to the caller.
func (w *Workspace) DeletePath(path string) FileOperationResultMsg {
	if w.FileOps == nil {
		w.FileOps = NewFileOpsService(w.RootPath, w.eventBus, w.Logger)
	}
	return w.FileOps.DeletePath(path)
}

// ApplyWorkspaceEdit applies a set of text edits to files on disk.
// This is used for LSP rename/refactor operations that modify multiple files.
// The result is returned directly to the caller.
func (w *Workspace) ApplyWorkspaceEdit(edits []FileEdit) FileOperationResultMsg {
	if w.FileOps == nil {
		w.FileOps = NewFileOpsService(w.RootPath, w.eventBus, w.Logger)
	}
	return w.FileOps.ApplyWorkspaceEdit(edits)
}

// SwitchProject re-roots this workspace at a new project directory.
// Closes open sessions and recreates every project-bound domain manager.
// Keeps sessionCfg, homeDir, sender, application defaults, and frontend
// connections so the TUI can continue without restarting.
func (w *Workspace) SwitchProject(projectDir string) error {
	if projectDir == "" {
		return fmt.Errorf("switch project: empty project directory")
	}
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return fmt.Errorf("switch project: resolve %q: %w", projectDir, err)
	}
	if err := validateProjectDirectory(abs); err != nil {
		return fmt.Errorf("switch project: %w", err)
	}
	if abs == w.RootPath {
		return nil
	}
	if w.HistoryService != nil && w.HistoryService.Busy() {
		return fmt.Errorf("switch project: local history capture or restore is active")
	}

	// Prepare all target persistence before touching current project state.
	// WorkspaceDir and OpenWorkspaceDB perform creation/migration, so success
	// here proves the replacement can persist state.
	wsDir, err := WorkspaceDir(abs)
	if err != nil {
		return fmt.Errorf("switch project: prepare workspace directory for %q: %w", abs, err)
	}
	wsDB, err := OpenWorkspaceDB(abs)
	if err != nil {
		if wsDB != nil {
			_ = wsDB.Close()
		}
		return fmt.Errorf("switch project: prepare workspace database for %q: %w", abs, err)
	}
	collaborationStore, err := collaboration.Attach(
		wsDB.DB, canonicalizePath(abs), filepath.Join(wsDir, "collaboration-locks"),
	)
	if err != nil {
		_ = wsDB.Close()
		return fmt.Errorf("switch project: prepare collaboration runtime for %q: %w", abs, err)
	}
	historyStore, err := openLocalHistory(abs, wsDir)
	if err != nil {
		_ = collaborationStore.Close()
		_ = wsDB.Close()
		return fmt.Errorf("switch project: prepare local history for %q: %w", abs, err)
	}
	dbTransferred := false
	defer func() {
		if !dbTransferred {
			if historyStore != nil {
				_ = historyStore.Close()
			}
			_ = collaborationStore.Close()
			_ = wsDB.Close()
		}
	}()

	// ProjectSearchService intentionally survives the switch after Cancel:
	// it stores no project root, and retaining its monotonically increasing
	// request IDs prevents a delayed old-project completion from colliding
	// with the first request in the new project.
	w.CancelProjectSearch()

	// Tear down old project state.
	w.capabilityMu.Lock()
	defer w.capabilityMu.Unlock()
	w.CloseAllSessions()
	if w.Capabilities != nil {
		w.Capabilities.Close()
	}
	w.Capabilities = nil
	w.FileOwnership = agent.NewFileOwnership()
	if w.TermMgr != nil {
		w.TermMgr.SetEventBus(nil)
		w.TermMgr.CloseAll()
	}
	if w.LSPMgr != nil {
		w.LSPMgr.SetOnDiagnostics(nil)
		w.LSPMgr.Stop()
	}
	if w.Status != nil {
		w.Status.Close()
		w.Status = nil
	}
	if w.registryCostWriter != nil {
		w.registryCostWriter.Close()
		w.registryCostWriter = nil
	}
	// Sessions must detach while their collaboration database is still open.
	// Release the old service before closing its borrowed SQL handle; never
	// carry it into the replacement project's sessions.
	if w.Collaboration != nil {
		_ = w.Collaboration.Close()
		w.Collaboration = nil
	}
	if w.WorkspaceDB != nil {
		_ = w.WorkspaceDB.Close()
		w.WorkspaceDB = nil
	}
	if w.HistoryService != nil {
		w.HistoryService.Stop()
		w.HistoryService = nil
	}
	if w.LocalHistory != nil {
		_ = w.LocalHistory.Close()
		w.LocalHistory = nil
	}

	// Rebuild around the new project.
	w.RootPath = abs
	w.WorkspaceDir = wsDir
	w.WorkspaceDB = wsDB
	w.workspaceDBErr = nil
	w.Collaboration = collaborationStore
	w.collaborationErr = nil
	w.LocalHistory = historyStore
	w.HistoryService = NewLocalHistoryService(abs, historyStore, w.Logger)
	w.localHistoryErr = nil
	dbTransferred = true

	w.LSPMgr = NewLSPManager(w.Logger)
	w.AgentMgr = NewAgentManager()
	w.PaneMgr = NewPaneManager()
	if w.ProjectSearch == nil {
		w.ProjectSearch = NewProjectSearchService()
	}
	w.FileOps = NewFileOpsService(abs, w.eventBus, w.Logger)
	w.Git = NewGitService(abs)
	if w.Documents != nil {
		w.Documents.Close()
	}
	w.Documents = NewDocumentSaveService()
	w.Documents.SetEventBus(w.eventBus)
	w.TermMgr = NewTerminalManager(abs)
	w.Status = NewStatusCenter(workspaceSQLDB(wsDB), w.Logger)
	w.registryCostWriter = newRegistryCostWriter(wsDB, w.Logger)
	status := w.Status
	bus := w.eventBus
	go func() {
		if err := status.Hydrate(); err != nil {
			if w.Logger != nil {
				w.Logger.Warn("STATUS", "notification_hydrate_failed", "error", err.Error())
			}
			return
		}
		if bus != nil {
			bus.Send(StatusCenterHydratedMsg{})
		}
	}()
	w.FocusStack = NewFocusStack()
	w.registryErr = nil
	if w.eventBus != nil {
		w.TermMgr.SetEventBus(w.eventBus)
	}
	w.applyEffectiveSettings()

	// Same self-heal as NewWorkspace when switching projects.
	_ = w.BackfillSessionRegistry()

	return nil
}

func workspaceSQLDB(db *WorkspaceDB) *sql.DB {
	if db == nil {
		return nil
	}
	return db.DB
}

func validateProjectDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect project directory %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project target %q is not a directory", path)
	}

	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open project directory %q: %w", path, err)
	}
	_, readErr := dir.Readdirnames(1)
	closeErr := dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("read project directory %q: %w", path, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close project directory %q after validation: %w", path, closeErr)
	}
	return nil
}

// LoadAppSettings returns the effective settings for this workspace:
// application defaults overlaid with workspace overrides.
func (w *Workspace) LoadAppSettings() ttypes.AppSettings {
	return w.EffectiveAppSettings()
}

// SetBackendConfig stores the session config and home directory.
// Called by app.Run() after creating the workspace.
func (w *Workspace) SetBackendConfig(cfg *session.Config, homeDir string) {
	effective := w.EffectiveAppSettings()
	if len(effective.Agents.Categories) > 0 {
		session.ApplyWorkspaceAgentDefaults(cfg, effective.Agents)
	}
	cfg.Memory = effective.Memory
	w.sessionCfg = cfg
	w.homeDir = homeDir
}

// SetEventSender stores the EventSender (TUI) for orchestrator creation.
// Called by app.Run() after creating the TUI.
func (w *Workspace) SetEventSender(sender session.EventSender) {
	w.sender = sender
}

// NewSessionFromLauncher creates a new session and starts its orchestrator.
// Called by the launcher when the user selects "New Session".
func (w *Workspace) NewSessionFromLauncher() *NewSessionResult {
	if w.sessionCfg == nil {
		w.Logger.Warn("WORKSPACE", "no_session_config")
		return &NewSessionResult{Error: "Session configuration is unavailable"}
	}
	if w.sender == nil {
		return &NewSessionResult{Error: "Session event transport is unavailable"}
	}
	previous := w.ActiveSession
	_, err := w.CreateSession(w.sessionCfg, w.homeDir)
	if err != nil {
		w.Logger.Warn("WORKSPACE", "new_session_failed", "error", err.Error())
		return &NewSessionResult{Error: "Failed to create session: " + err.Error(), NeedsAccount: errors.Is(err, session.ErrAccountRequired)}
	}
	orch, err := w.StartOrchestrator(w.sender)
	if err != nil {
		w.Logger.Warn("WORKSPACE", "start_orchestrator_failed", "error", err.Error())
		w.abandonUnstartedSession(w.ActiveSession, previous)
		return &NewSessionResult{Error: "Failed to start session: " + err.Error()}
	}
	return &NewSessionResult{
		OrchID: orch.ID,
	}
}

// ResumeSessionFromLauncher resumes an existing session by ID and starts
// its orchestrator in-place (no process restart). Returns the failure reason.
// If the session is already open, it is activated and a new orchestrator
// is started for multi-session support.
func (w *Workspace) ResumeSessionFromLauncher(sessionID string) *NewSessionResult {
	if w.sessionCfg == nil {
		w.Logger.Warn("WORKSPACE", "no_session_config")
		return &NewSessionResult{Error: "Session configuration is unavailable"}
	}
	if w.sender == nil {
		return &NewSessionResult{Error: "Session event transport is unavailable"}
	}
	previous := w.ActiveSession
	created := false

	// Already open: activate its existing orchestrator when it still has one.
	if existing, ok := w.SessionsByID[sessionID]; ok && existing != nil {
		w.ActiveSession = existing
		if node := w.AgentMgr.Get(existing.AgentID); node != nil {
			return &NewSessionResult{
				OrchID: node.ID,
			}
		}
	} else {
		_, err := w.ResumeSession(sessionID, w.sessionCfg, w.homeDir)
		if err != nil {
			w.Logger.Warn("WORKSPACE", "resume_failed", "id", sessionID, "error", err.Error())
			return &NewSessionResult{Error: "Failed to resume session: " + err.Error(), NeedsAccount: errors.Is(err, session.ErrAccountRequired)}
		}
		created = true
	}
	orch, err := w.StartOrchestrator(w.sender)
	if err != nil {
		w.Logger.Warn("WORKSPACE", "start_orchestrator_failed", "error", err.Error())
		if created {
			w.abandonUnstartedSession(w.ActiveSession, previous)
		} else {
			w.ActiveSession = previous
		}
		return &NewSessionResult{Error: "Failed to start session: " + err.Error()}
	}

	// Hydrate history for the resumed session.
	w.Hydrate()

	return &NewSessionResult{
		OrchID: orch.ID,
	}
}

// ActivateSessionByAgent switches the workspace business context to the
// session backing an orchestrator tab.
func (w *Workspace) ActivateSessionByAgent(agentID string) {
	if sess := w.sessionForAgent(agentID); sess != nil {
		w.activateSession(sess)
		return
	}
	for _, sess := range w.Sessions {
		if sess != nil && sess.AgentID == agentID {
			w.activateSession(sess)
			return
		}
	}
}

func (w *Workspace) activateSession(sess *Session) {
	w.ActiveSession = sess
}
