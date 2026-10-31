package workspace

import (
	"context"
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
	captypes "crude/types/capability"
)

// NewSessionResult identifies the orchestrator created by a launcher action.
// Turn input, blocking responses, human steering, and cancellation are routed through
// generation-bound Workspace methods rather than cross-layer channels.
type NewSessionResult struct {
	SessionID    string
	OrchID       string
	Error        string
	NeedsAccount bool
	NeedsModel   bool
}

// Workspace is the project-scoped composition root for one IDE instance.
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
// The App struct (in tui/viewmodel) reaches a Workspace only through the
// shellport.Workspace interface (shellport.Local adapts this type).
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

	// ProjectContext owns passive artifact observations across shell attachments.
	// It is not document text, a second LSP fleet or an execution authority.
	ProjectContext *ProjectContextService

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
	Documents     *DocumentSaveService
	LiveDocuments *DocumentService
	Executions    *ExecutionService

	// TermMgr owns in-app PTY sessions (the terminal multiplexer).
	TermMgr *TerminalManager
	// Terminals owns the one parser/reply state for every PTY. Shells consume
	// immutable projections; they never parse a second reply-capable byte stream.
	Terminals *TerminalService
	// Status retains typed workspace-scoped lifecycle, activity, issue, and
	// notification projections. Resource managers remain authoritative.
	Status             *StatusCenter
	registryCostWriter *registryCostWriter

	// ── Sessions
	//
	// The workspace owns all AI conversations for this project. Sessions
	// is the canonical list; SessionsByID indexes by session ID for fast
	// lookup. Selection belongs to each shell, not this runtime.

	// Sessions is the canonical list of all frontend sessions in this workspace.
	Sessions []*Session

	// SessionsByID indexes sessions by their session ID.
	SessionsByID map[string]*Session

	// SessionsByAgent maps every live orchestrator to its frontend session.
	// A session may own more than one orchestrator over its lifetime.
	SessionsByAgent map[string]*Session

	// Agent admission and snapshots share the workspace's serialized owner.
	// These are runtime facts, never a client's selected tab or pane geometry.
	agentRevision uint64
	agentPrompts  map[string]AgentPrompt

	// ── Backend session config (set by app layer)
	//
	// These carry the backend knowledge needed to create/resume sessions
	// without callback indirection. Set once during app.Run().

	// sessionCfg is the session config derived from app config.
	sessionCfg *session.Config

	// appDefaults is the application-layer settings snapshot supplied by
	// app/ from ~/.crude/config.json. Workspace overrides layer on top.
	appDefaults ttypes.AppSettings
	// The factory worker and successful preference publications install this
	// observation. Actor reads never inspect the filesystem to derive settings.
	analysisRootIdentity string

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
	// Core commits return an immutable durable snapshot. Only the serialized
	// workspace owner publishes it; workers must never call SetAppDefaults.
	OnAppAccountCommit     func(ttypes.AccountChange) (ttypes.AppSettings, error)
	OnAppPreferencesCommit func(edits []ttypes.SettingEdit) (ttypes.AppSettings, error)
	// OnAppCapabilityRegistration persists one service-initiated application
	// capability registration. The app layer owns config.json, so it applies the
	// shared registration owner under its config lock rather than replacing
	// sibling registrations with a stale snapshot.
	OnAppCapabilityRegistration func(change captypes.RegistrationChange) (captypes.Settings, error)

	// ── Shared state (references, not owned)
	//
	// These are references to objects shared with the TUI layer.

	// Logger is the global logger.
	Logger Logger

	// InitialModelName is startup display metadata, not a selected session.
	InitialModelName string

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
	// must not fail because of this; it is surfaced for logging/UI only.
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
	if w.LiveDocuments != nil {
		w.LiveDocuments.SetEventBus(bus)
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
	lspMgr.SetWorkspaceRoot(rootPath)

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
		Sessions:           make([]*Session, 0),
		SessionsByID:       make(map[string]*Session),
		SessionsByAgent:    make(map[string]*Session),
		Logger:             lg,
		InitialModelName:   modelName,
		InitialSettings:    initial,
		Username:           username,
		StartTime:          time.Now(),
		workspaceDBErr:     dbErr,
	}
	if historyStore != nil {
		ws.HistoryService = NewLocalHistoryService(rootPath, historyStore, lg)
	}
	ws.Terminals, _ = NewTerminalService(ws.TermMgr) // newly allocated, empty manager
	ws.LiveDocuments = NewDocumentService(rootPath, ws.Documents, ws.LSPMgr, workspaceSQLDB(wsDB))
	ws.ProjectContext = NewProjectContextService(rootPath, ws.LiveDocuments.Insight.generation)

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
	if w.LiveDocuments != nil {
		w.LiveDocuments.mu.Lock()
		if w.LiveDocuments.stateDB == nil && len(w.LiveDocuments.docs) == 0 {
			w.LiveDocuments.stateErr = w.LiveDocuments.attachState(db.DB)
		}
		w.LiveDocuments.mu.Unlock()
	}
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
	if err := CloseExecutionHistory(w); err != nil {
		w.Logger.Warn("HISTORY", "execution_history_close_failed", "error", err.Error())
	}
	if w.LiveDocuments != nil {
		w.LiveDocuments.Close()
	}
	if w.Documents != nil {
		w.Documents.Close()
	}
	if w.Capabilities != nil {
		w.Capabilities.Close()
	}
	if w.Terminals != nil {
		w.Terminals.CloseAll()
	} else if w.TermMgr != nil {
		w.TermMgr.CloseAll()
	}
	if w.LSPMgr != nil {
		w.LSPMgr.Close()
	}
	if w.ProjectContext != nil {
		w.ProjectContext.Close()
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
			// Close joined finalization and closed the borrowed session handle.
			// Read the final on-disk metadata, including shutdown-preserved input.
			w.persistSessionRegistry(fs, nil)
		}
	}
	w.Sessions = w.Sessions[:0]
	w.SessionsByID = make(map[string]*Session)
	w.SessionsByAgent = make(map[string]*Session)
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
func (w *Workspace) persistSessionRegistry(fs *Session, sessionDB *sql.DB) {
	if w == nil || w.WorkspaceDB == nil || fs == nil || fs.SessionID == "" {
		return
	}
	if fs.backend != nil && fs.backend.Pruned() {
		// The backend owns retention: an empty session removed its storage, so
		// the registry forgets it rather than resurrecting a row for a deleted DB.
		if err := w.WorkspaceDB.ForgetSession(fs.SessionID); err != nil {
			w.noteRegistryErr("session_forget_failed", err)
		}
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
		// Still attempt meta sync / close stamp; Register may have failed for
		// an unrelated reason while the row already exists.
	} else {
		w.noteRegistryErr("", nil)
	}
	if sessionDB != nil {
		if err := w.WorkspaceDB.syncSessionMetaFromDB(sessionDB, fs.SessionID); err != nil {
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
	if w.LiveDocuments != nil {
		w.LiveDocuments.Close()
	}
	if w.TermMgr != nil {
		w.TermMgr.SetEventBus(nil)
		if w.Terminals != nil {
			w.Terminals.CloseAll()
		} else {
			w.TermMgr.CloseAll()
		}
	}
	if w.LSPMgr != nil {
		w.LSPMgr.Close()
	}
	if w.ProjectContext != nil {
		w.ProjectContext.Close()
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
	w.analysisRootIdentity = ""
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
	w.LSPMgr.SetWorkspaceRoot(abs)
	w.AgentMgr = NewAgentManager()
	w.agentPrompts = nil
	w.agentRevision++
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
	w.LiveDocuments = NewDocumentService(abs, w.Documents, w.LSPMgr, workspaceSQLDB(wsDB))
	w.ProjectContext = NewProjectContextService(abs, w.LiveDocuments.Insight.generation)
	w.Executions = nil
	w.LiveDocuments.SetEventBus(w.eventBus)
	w.TermMgr = NewTerminalManager(abs)
	w.Terminals, _ = NewTerminalService(w.TermMgr)
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
	w.registryErr = nil
	if w.eventBus != nil {
		w.TermMgr.SetEventBus(w.eventBus)
	}
	w.applyEffectiveSettings()

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

// NewAgentSession creates a session and registers its runtime without selecting
// it in any shell. Callers must serialize workspace lifecycle operations.
func (w *Workspace) NewAgentSession() *NewSessionResult {
	return w.openAgentSessionOperation("")
}

// OpenAgentSession opens an exact saved session or returns its existing runtime.
// Opening never selects a client view or starts a provider turn.
func (w *Workspace) OpenAgentSession(sessionID string) *NewSessionResult {
	return w.openAgentSessionOperation(sessionID)
}

func (w *Workspace) openAgentSessionOperation(id string) *NewSessionResult {
	op, err := w.PrepareAgentSession(id)
	if err != nil {
		return &NewSessionResult{Error: err.Error(), NeedsAccount: errors.Is(err, session.ErrAccountRequired), NeedsModel: errors.Is(err, session.ErrModelRequired)}
	}
	return w.ApplyAgentSession(op.Run(context.Background()))
}
