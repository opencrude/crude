package workspace

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"crude/provider"
	"crude/session"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
	"crude/tui/window"
)

// NewSessionResult carries the orchestrator's channels after a new session
// is created via the OnNewSession callback. Will be absorbed into workspace
// methods in Phase 4.
type NewSessionResult struct {
	OrchID         string
	InputCh        chan<- provider.UserInput
	BtwCh          chan<- provider.UserInput
	CancelCh       chan<- struct{}
	BlockResolveCh chan string
}

// Workspace is the project-scoped composition root — the IDE instance.
//
// One Workspace per project directory, inspired by JetBrains' project model.
// Each workspace gets:
//   - A dedicated folder under ~/.crude/workspaces/<hash>/ (hash of project dir)
//   - Its own SQLite database for workspace-level metadata
//   - Its own file tree, LSP connections, editor tabs, agent sessions
//
// The workspace owns ALL business state for one IDE instance:
//   - AI agent sessions and orchestrators
//   - Editor buffers and tab state
//   - LSP server connections and diagnostics cache
//   - File tree model
//   - Workspace-specific settings (inheriting from app config)
//
// The App struct (in tui/viewmodel) embeds *Workspace and delegates to it.
// Multiple workspaces can coexist for multi-project support.
//
// Session-scoped state (chat input, per-agent dialogs, message lists, cost
// tracking) lives on Session, one per agent conversation. The Workspace
// holds the agent tree (AgentManager) which links to Sessions via AgentNode.State.
type Workspace struct {
	// RootPath is the absolute path to the project directory this workspace
	// is rooted at. The file tree, LSP manager, and file watcher all key
	// off this path.
	RootPath string

	// ── Workspace-level persistence ───────────────────────────────────
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

	// ── Managers (workspace-scoped) ───────────────────────────────────
	//
	// Each manager is the single source of truth for its concern. All are
	// created in NewWorkspace and live for the workspace's lifetime.

	// AgentMgr owns the agent tree (orchestrator + sub-agents). Each
	// AgentNode.State is a *Session (frontend session for one agent).
	AgentMgr *AgentManager

	// PaneMgr owns the sub-pane display state (thinking panes, agent panes).
	// Sub-panes are rendered inline in the parent agent's message list.
	PaneMgr *PaneManager

	// BufferMgr owns all open file buffers (PieceTable data). Multiple
	// editor views can share the same buffer for split/diff views.
	BufferMgr *editor.BufferManager

	// LSPMgr owns all LSP server connections for this workspace's root
	// directory. One LSPManager per workspace (rooted at RootPath).
	LSPMgr *LSPManager

	// FocusStack manages keyboard focus as a stack. Pushing a floating
	// window or clicking the tree pushes focus; closing pops back.
	FocusStack *FocusStack

	// Tree owns the file tree model, renderer, and layout state.
	// The model and renderer are data (file system contents); the layout
	// fields are display state used by the viewmodel render pipeline.
	Tree *TreeState

	// ── Tab bars (workspace-scoped) ───────────────────────────────────
	//
	// Win is the main tab bar (editor tabs + agent tabs).
	// Overlay is the overlay tab bar (settings/confirm/question/yolo tabs).
	Win     *window.Window
	Overlay *window.Window

	// ── Workspace-scoped popups & prompts ─────────────────────────────
	//
	// These are transient UI elements tied to the workspace's editors and
	// file tree. They live on the workspace, not the session.

	// FindReplace is the find/replace popup state for the active editor.
	// nil when the popup is closed.
	FindReplace *widgets.FindReplaceState

	// LastContextMenu is the current right-click context menu state.
	// nil when no context menu is open.
	LastContextMenu *widgets.ContextMenuState

	// ContextMenuOpenedAt is the timestamp when the context menu was
	// opened, used for the slide-in animation.
	ContextMenuOpenedAt time.Time

	// TreePrompt is the file-tree text prompt (new file, rename, etc.).
	// nil when no prompt is open.
	TreePrompt *widgets.TextPromptState

	// TreeDeletePending is the path of the file/folder pending deletion
	// confirmation. Empty when no delete is pending.
	TreeDeletePending string

	// FilePicker is the file picker widget (attach file + new workspace modes).
	// It's a value, not a pointer — the widget manages its own internal state.
	FilePicker widgets.FilePicker

	// DiagPopupID is the ID of the diagnostic hover popup's floating window.
	// Empty when no diagnostic popup is open.
	DiagPopupID string

	// DirtyPromptPath is the path of the editor tab pending dirty-close
	// confirmation. Empty when no dirty prompt is open.
	DirtyPromptPath string

	// DirtyPromptChoice tracks the user's selection in the dirty-close
	// prompt (0 = No/discard, 1 = Yes/save).
	DirtyPromptChoice int

	// ChatAreaHeight is the height of the chat area in the current layout.
	// Used by message list rendering to size the chat view.
	ChatAreaHeight int

	// LastInteractionTime tracks the last time the user interacted with
	// this workspace (used for the launcher timeout).
	LastInteractionTime time.Time

	// ── Sessions ──────────────────────────────────────────────────────
	//
	// The workspace owns all AI conversations for this project. Sessions
	// is the canonical list; SessionsByID indexes by session ID for fast
	// lookup. ActiveSession points to the conversation currently displayed
	// in the chat view. nil when the launcher/workspace selector is showing.

	// Sessions is the canonical list of all frontend sessions in this workspace.
	Sessions []*Session

	// SessionsByID indexes sessions by their session ID.
	SessionsByID map[string]*Session

	// ActiveSession is the currently-active frontend session.
	ActiveSession *Session

	// ── Backend session config (set by app layer) ────────────────────────
	//
	// These carry the backend knowledge needed to create/resume sessions
	// without callback indirection. Set once during app.Run().

	// sessionCfg is the session config derived from app config.
	sessionCfg *session.Config

	// homeDir is the user's home directory.
	homeDir string

	// sender is the EventSender (the TUI) for starting orchestrators.
	// Set after TUI creation.
	sender session.EventSender

	// OnConfigSave is called when provider credentials change so the app
	// layer can persist them to config.json. Optional.
	OnConfigSave func(cfg *session.Config) error

	// ── Shared state (references, not owned) ───────────────────────────
	//
	// These are references to objects shared with the TUI layer.

	// Theme is the global color/style theme.
	Theme *theme.Theme

	// SessionSettings is the session-scoped AI settings model.
	SessionSettings *widgets.SessionSettings

	// StatusBar is the global status bar.
	StatusBar *widgets.StatusBar

	// Logger is the global logger.
	Logger Logger

	// Input is the chat input bar.
	Input input.Model

	// ModelName is the display name of the active model.
	// Set during workspace creation, updated by the TUI layer.
	ModelName string

	// Username is the resolved username (for message attribution).
	Username string

	// StartTime is when this workspace was created.
	StartTime time.Time

	// SpinFrame is the current spinner animation frame.
	SpinFrame int

	// History is the input command history.
	History []string

	// LastSettings is the last-applied settings snapshot.
	LastSettings ttypes.Settings

	// FormHint is the current contextual form hint shown in the status bar.
	FormHint string
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
	th := theme.DefaultDark()
	im := input.NewModel(th)
	sb := widgets.NewStatusBar(th)

	rendering.SetInvalidUTF8Logger(func(tag, msg string, pairs ...any) { lg.Warn(tag, msg, pairs...) })

	if rootPath == "" {
		rootPath, _ = os.Getwd()
	}

	treeModel := filetree.NewFileTreeModel(rootPath)
	treeRenderer := filetree.NewTreeRenderer(treeModel, treeModel.IconResolver())
	treeRenderer.ApplyTheme(th)

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

	ws := &Workspace{
		RootPath:        rootPath,
		WorkspaceDir:    wsDir,
		WorkspaceDB:     wsDB,
		Theme:           th,
		AgentMgr:        NewAgentManager(),
		PaneMgr:         NewPaneManager(),
		BufferMgr:       editor.NewBufferManager(),
		LSPMgr:          lspMgr,
		FocusStack:      NewFocusStack(),
		Tree:            NewTreeState(treeModel, treeRenderer, th.Tree.Config.DefaultWidth),
		Win:             window.New(th),
		Overlay:         window.New(th),
		FilePicker:      widgets.NewFilePicker(th),
		Sessions:        make([]*Session, 0),
		SessionsByID:    make(map[string]*Session),
		SessionSettings: widgets.NewSessionSettings(th, initial),
		StatusBar:       sb,
		Logger:          lg,
		Input:           im,
		ModelName:       modelName,
		Username:        username,
		StartTime:       time.Now(),
		LastSettings:    initial,
	}

	return ws
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

// ReportPath returns the path to the reports directory for the active session.
// Used by sub-agent report writing.
func (w *Workspace) ReportPath() string {
	if w.ActiveSession != nil {
		return filepath.Join(w.ActiveSession.SessionDir, "reports")
	}
	return ""
}

// Close shuts down the workspace and releases all resources.
// This includes closing the workspace database and stopping LSP connections.
// Called when the IDE instance is shutting down.
func (w *Workspace) Close() {
	w.CloseAllSessions()
	if w.LSPMgr != nil {
		w.LSPMgr.Stop()
	}
	if w.WorkspaceDB != nil {
		w.WorkspaceDB.Close()
		w.WorkspaceDB = nil
	}
}

// CloseAllSessions closes every open backend session.
func (w *Workspace) CloseAllSessions() {
	for _, fs := range w.Sessions {
		if fs != nil && fs.backend != nil {
			fs.backend.Close()
		}
		if w.WorkspaceDB != nil && fs != nil && fs.SessionID != "" {
			_ = w.WorkspaceDB.CloseSession(fs.SessionID)
		}
	}
	w.Sessions = w.Sessions[:0]
	w.SessionsByID = make(map[string]*Session)
	w.ActiveSession = nil
}

// SwitchProject re-roots this workspace at a new project directory.
// Closes open sessions and reopens the workspace DB/tree for the new path.
// Keeps sessionCfg, homeDir, sender, and theme so the TUI can continue.
func (w *Workspace) SwitchProject(projectDir string) error {
	if projectDir == "" {
		return fmt.Errorf("empty project dir")
	}
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		abs = projectDir
	}
	if abs == w.RootPath {
		return nil
	}

	// Tear down old project state.
	w.CloseAllSessions()
	if w.LSPMgr != nil {
		w.LSPMgr.Stop()
	}
	if w.WorkspaceDB != nil {
		w.WorkspaceDB.Close()
		w.WorkspaceDB = nil
	}

	// Rebuild around the new project.
	w.RootPath = abs
	wsDir, wsErr := WorkspaceDir(abs)
	if wsErr != nil {
		return wsErr
	}
	w.WorkspaceDir = wsDir

	wsDB, dbErr := OpenWorkspaceDB(abs)
	if dbErr != nil {
		w.Logger.Warn("WORKSPACE", "db_open_failed", "error", dbErr.Error())
	}
	w.WorkspaceDB = wsDB

	treeModel := filetree.NewFileTreeModel(abs)
	treeRenderer := filetree.NewTreeRenderer(treeModel, treeModel.IconResolver())
	treeRenderer.ApplyTheme(w.Theme)
	w.Tree = NewTreeState(treeModel, treeRenderer, w.Theme.Tree.Config.DefaultWidth)

	w.LSPMgr = NewLSPManager(w.Logger)
	w.BufferMgr = editor.NewBufferManager()
	w.AgentMgr = NewAgentManager()
	w.PaneMgr = NewPaneManager()

	return nil
}

// LoadAppSettings returns the app settings stored in workspace.db,
// or an empty AppSettings if none are stored.
func (w *Workspace) LoadAppSettings() ttypes.AppSettings {
	if w.WorkspaceDB == nil {
		return ttypes.AppSettings{}
	}
	raw := w.WorkspaceDB.GetSetting("app_settings")
	if raw == "" {
		return ttypes.AppSettings{}
	}
	var s ttypes.AppSettings
	json.Unmarshal([]byte(raw), &s)
	return s
}

// SetBackendConfig stores the session config and home directory.
// Called by app.Run() after creating the workspace.
func (w *Workspace) SetBackendConfig(cfg *session.Config, homeDir string) {
	if appSettings := w.LoadAppSettings(); len(appSettings.Agents.Categories) > 0 {
		session.ApplyWorkspaceAgentDefaults(cfg, appSettings.Agents)
	}
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
		return nil
	}
	_, err := w.CreateSession(w.sessionCfg, w.homeDir)
	if err != nil {
		w.Logger.Warn("WORKSPACE", "new_session_failed", "error", err.Error())
		return nil
	}
	if w.sender == nil {
		w.Logger.Warn("WORKSPACE", "no_event_sender")
		return nil
	}
	orch, err := w.StartOrchestrator(w.sender)
	if err != nil {
		w.Logger.Warn("WORKSPACE", "start_orchestrator_failed", "error", err.Error())
		return nil
	}
	return &NewSessionResult{
		OrchID:         orch.ID,
		InputCh:        orch.InputCh,
		BtwCh:          orch.BtwCh,
		CancelCh:       orch.CancelCh,
		BlockResolveCh: orch.BlockResolveCh,
	}
}

// ResumeSessionFromLauncher resumes an existing session by ID and starts
// its orchestrator in-place (no process restart). Returns nil on failure.
// If the session is already open, it is activated and a new orchestrator
// is started for multi-session support.
func (w *Workspace) ResumeSessionFromLauncher(sessionID string) *NewSessionResult {
	if w.sessionCfg == nil {
		w.Logger.Warn("WORKSPACE", "no_session_config")
		return nil
	}

	// Already open: switch ActiveSession and start a new orchestrator tab.
	if existing, ok := w.SessionsByID[sessionID]; ok && existing != nil {
		w.ActiveSession = existing
		if existing.Settings != nil {
			w.SessionSettings = existing.Settings
		}
		w.Input.SetReferenceStore(existing.RefStore)
	} else {
		_, err := w.ResumeSession(sessionID, w.sessionCfg, w.homeDir)
		if err != nil {
			w.Logger.Warn("WORKSPACE", "resume_failed", "id", sessionID, "error", err.Error())
			return nil
		}
	}

	if w.sender == nil {
		w.Logger.Warn("WORKSPACE", "no_event_sender")
		return nil
	}
	orch, err := w.StartOrchestrator(w.sender)
	if err != nil {
		w.Logger.Warn("WORKSPACE", "start_orchestrator_failed", "error", err.Error())
		return nil
	}

	// Hydrate history for the resumed session.
	w.Hydrate()

	return &NewSessionResult{
		OrchID:         orch.ID,
		InputCh:        orch.InputCh,
		BtwCh:          orch.BtwCh,
		CancelCh:       orch.CancelCh,
		BlockResolveCh: orch.BlockResolveCh,
	}
}

// ActivateSessionByAgent switches the workspace business context to the
// session backing an orchestrator tab.
func (w *Workspace) ActivateSessionByAgent(agentID string) {
	for _, sess := range w.Sessions {
		if sess != nil && sess.AgentID == agentID {
			w.ActiveSession = sess
			if sess.Settings != nil {
				w.SessionSettings = sess.Settings
			}
			if sess.RefStore != nil {
				w.Input.SetReferenceStore(sess.RefStore)
			}
			return
		}
	}
}
