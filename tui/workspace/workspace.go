package workspace

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/settings"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
	"crude/tui/window"
)

// NewSessionResult is returned by the OnNewSession callback. It carries
// the orchestrator's ID and the channels needed to communicate with the
// backend session goroutine.
type NewSessionResult struct {
	OrchID         string
	InputCh        chan<- provider.UserInput
	BtwCh          chan<- provider.UserInput
	CancelCh       chan<- struct{}
	BlockResolveCh chan string
}

// Workspace is the project-scoped composition root. One Workspace per
// project directory. It owns everything that is rooted at that directory:
// the file tree, the LSP manager, the editor tabs, the window manager
// (for floating popups), the tab bar, the focus stack, and the agent tree.
//
// The App struct (in tui/viewmodel) embeds *Workspace and delegates to it.
// Multiple workspaces can coexist in a future multi-project IDE; today the
// App creates exactly one.
//
// Session-scoped state (chat input, per-agent dialogs, message lists, cost
// tracking) does NOT live here — it lives on Session, one per agent
// conversation. The Workspace holds the agent tree (AgentManager) which
// links to Sessions via AgentNode.State.
type Workspace struct {
	// RootPath is the absolute path to the project directory this workspace
	// is rooted at. The file tree, LSP manager, and file watcher all key
	// off this path.
	RootPath string

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

	// LSPMgr owns all LSP server connections for this workspace's root
	// directory. One LSPManager per workspace (rooted at RootPath).
	LSPMgr *LSPManager

	// FocusStack manages keyboard focus as a stack. Pushing a floating
	// window or clicking the tree pushes focus; closing pops back.
	FocusStack *FocusStack

	// ── File tree (workspace-scoped, rooted at RootPath) ──────────────

	Tree *TreeState

	// ── Editor tabs (workspace-scoped) ────────────────────────────────
	//
	// EditorState holds the map of open editor tabs + the active editor.
	// Each tab wraps an *editor.Editor with per-tab cursor/scroll/undo.
	EditorState *EditorState

	// ── Tab bars (workspace-scoped) ───────────────────────────────────
	//
	// Win is the main tab bar (editor tabs + agent tabs).
	// Overlay is the overlay tab bar (settings/confirm/question/yolo tabs).
	Win     *window.Window
	Overlay *window.Window

	// ── Overlay mode (workspace-scoped) ───────────────────────────────
	//
	// OverlayState tracks which overlay mode is active (chat, settings,
	// confirm, question, yolo, text overlay, file picker). This is
	// workspace-scoped because the overlay renders on top of the
	// workspace's main view.
	OverlayState *OverlayState

	// ── LSP popup state (workspace-scoped) ────────────────────────────
	//
	// LSPPopup tracks the state of LSP popups (references, outline,
	// completion, rename) for the active editor in this workspace.
	LSPPopup *LSPPopupState

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

	// FilePicker is the file picker widget (used for adding file references).
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

	// ── Session-scoped singletons (temporary) ─────────────────────────
	//
	// These fields are session-scoped but currently stored as singletons
	// because the app only has one active session at a time. They will
	// move to the Session struct in a follow-up step. The Workspace
	// constructor creates them and the App accesses them directly.
	//
	// DO NOT add new session-scoped state here. New sessions should use
	// the Session struct.

	// ActiveSession is the currently-active frontend session. This is
	// the session whose agent conversation is displayed in the chat view.
	// nil when the launcher is showing (no session active).
	ActiveSession *Session

	// SessionsByID indexes all sessions by agent ID. The active session
	// is always present here (keyed by its orchestrator's agent ID).
	// Sub-agent sessions are also indexed here.
	SessionsByID map[string]*Session

	// ── Callbacks (wired by app.App via tui.TUI) ──────────────────────
	//
	// These are function pointers set by the app layer. They bridge the
	// TUI to the backend session.Session. Session-scoped callbacks
	// (OnConfirmResult, OnQuestionResult, OnCancelSubAgent) live on
	// Session, not here.

	// OnNewSession creates a new orchestrator on the backend and returns
	// its channels. Called when the user clicks "New Session" in the launcher.
	OnNewSession func() *NewSessionResult

	// OnResumeSession requests resumption of a past session by ID.
	// The app layer handles the actual session switch.
	OnResumeSession func(sessionID string)

	// OnSettingsSave persists app-level settings to disk.
	OnSettingsSave func(ttypes.Settings) error

	// OnHardCancel forcefully cancels all agents in the backend session.
	OnHardCancel func()

	// FetchModelsFn fetches available models from a provider's API.
	FetchModelsFn func(providerID, baseURL, apiKey string) ([]ttypes.ModelEntry, error)

	// GetProviderConfigFn returns the config (API key, URL) for a provider.
	GetProviderConfigFn func(string) ttypes.ProviderConfig

	// ProviderPresets is the static list of known providers for the settings UI.
	ProviderPresets []ttypes.ProviderPreset

	// OnDeleteSession deletes a session from disk by ID.
	OnDeleteSession func(sessionID string)

	// OnListSessions returns metadata for recent sessions (for the launcher).
	OnListSessions func() []sessiondb.SessionMeta

	// ── Shared, app-provided state (references, not owned) ────────────
	//
	// These are references to app-scoped objects. The workspace does not
	// own them — the App owns them and passes them in. They're here so
	// workspace methods can access them without a circular dependency.

	// Theme is the global color/style theme (app-scoped, shared).
	Theme *theme.Theme

	// Settings is the global settings model (app-scoped, shared).
	Settings settings.Model

	// StatusBar is the global status bar (app-scoped, shared).
	StatusBar *widgets.StatusBar

	// Logger is the global logger (app-scoped, shared).
	Logger Logger

	// Input is the chat input bar. Currently a singleton (one per app).
	// Will become per-session when Session is fully wired.
	Input input.Model

	// ModelName is the display name of the active model.
	ModelName string

	// Username is the resolved username (for message attribution).
	Username string

	// StartTime is when this workspace was created.
	StartTime time.Time

	// SpinFrame is the current spinner animation frame (global).
	SpinFrame int

	// History is the input command history (global, persisted to disk).
	History []string

	// LastSettings is the last-applied settings snapshot.
	LastSettings ttypes.Settings

	// FormHint is the current form hint text shown in the overlay.
	FormHint string

	// TokenPrompt/TokenCompletion/TokenCached are the token usage counters
	// for the active session's last LLM call. Displayed in the status bar.
	// Will move to Session when fully wired.
	TokenPrompt     int
	TokenCompletion int
	TokenCached     int

	// SessionCost is the cumulative cost (USD) for the active session.
	// Will move to Session.
	SessionCost float64

	// ContextWindow/ContextUsed track the context window usage.
	// Will move to Session.
	ContextWindow int
	ContextUsed   int

	// TokenEstimated is true when the token count is an estimate.
	TokenEstimated bool
}

// NewWorkspace creates a workspace rooted at the given directory.
//
// Parameters:
//   - rootPath: the project directory (file tree root, LSP root). If empty,
//     the current working directory is used.
//   - modelName, username: display metadata.
//   - initial: the initial settings snapshot.
//   - onSave: callback to persist settings (wired by app.App).
//   - getProviderConfigFn: callback to look up provider config.
//   - lg: the logger.
//   - sessions: session metadata for the launcher.
//   - sessionID, sessionDir, db: the active session's persistence handles
//     (used to create the reference store). These will move to Session
//     when fully wired; for now they create the singleton session's ref store.
func NewWorkspace(
	rootPath string,
	modelName string,
	username string,
	initial ttypes.Settings,
	onSave func(ttypes.Settings) error,
	getProviderConfigFn func(string) ttypes.ProviderConfig,
	lg Logger,
	sessions []sessiondb.SessionMeta,
	sessionID, sessionDir string,
	db *sql.DB,
) *Workspace {
	th := theme.DefaultDark()
	im := input.NewModel(th)
	sb := widgets.NewStatusBar(th)
	sm := settings.NewModel(th, initial, nil, getProviderConfigFn, nil)

	rendering.SetInvalidUTF8Logger(func(tag, msg string, pairs ...any) { lg.Warn(tag, msg, pairs...) })

	if rootPath == "" {
		rootPath, _ = os.Getwd()
		if rootPath == "" {
			rootPath = sessionDir
		}
	}

	treeModel := filetree.NewFileTreeModel(rootPath)
	treeRenderer := filetree.NewTreeRenderer(treeModel, treeModel.IconResolver())
	treeRenderer.ApplyTheme(th)

	lspMgr := NewLSPManager(lg)

	ws := &Workspace{
		RootPath:      rootPath,
		Theme:         th,
		AgentMgr:      NewAgentManager(),
		PaneMgr:       NewPaneManager(),
		LSPMgr:        lspMgr,
		FocusStack:    NewFocusStack(),
		Tree:          NewTreeState(treeModel, treeRenderer, th.Tree.Config.DefaultWidth),
		EditorState:   NewEditorState(),
		Win:           window.New(th),
		Overlay:       window.New(th),
		OverlayState:  NewOverlayState(),
		LSPPopup:      NewLSPPopupState(),
		FilePicker:    widgets.NewFilePicker(th),
		SessionsByID:  make(map[string]*Session),
		Settings:      sm,
		StatusBar:     sb,
		Logger:        lg,
		Input:         im,
		ModelName:     modelName,
		Username:      username,
		StartTime:     time.Now(),
		LastSettings:  initial,
		OnSettingsSave: onSave,
		GetProviderConfigFn: getProviderConfigFn,
	}

	// The editor singleton — created once, reused as the active editor.
	// Per-tab editors are created in openEditorTab and stored in EditorState.Tabs.
	ws.EditorState.Editor = editor.New(th)

	// Create the initial (singleton) session with the given persistence handles.
	// This session holds the reference store and the per-agent dialogs.
	// In a future step, this will be created per-orchestrator instead.
	ws.ActiveSession = NewSession(th, sessionID, sessionDir, db, lg)
	ws.Input.SetReferenceStore(ws.ActiveSession.RefStore)

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