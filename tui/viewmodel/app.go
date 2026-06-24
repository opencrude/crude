package viewmodel

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
	"crude/provider"
	"crude/sessiondb"
	"crude/tui/compositor"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/settings"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/widget"
	"crude/tui/widgets"
	"crude/tui/window"
	"crude/tui/wm"
)

var OnHistorySave func([]string)

type Mode int

const (
	ModeChat Mode = iota
	ModeSettings
	ModeConfirm
	ModeQuestion
	ModeYolo
	ModeTextOverlay
	ModeFilePicker
)

type Logger interface {
	Info(tag, msg string, pairs ...any)
	Warn(tag, msg string, pairs ...any)
	Error(tag, msg string, pairs ...any)
	Debug(tag, msg string, pairs ...any)
}

type App struct {
	theme  *theme.Theme
	Mode   Mode
	width  int
	height int

	agents       map[string]*AgentState
	paneMgr      *PaneManager
	input        input.Model
	statusbar    *widgets.StatusBar
	settings     settings.Model
	confirm      widgets.ConfirmDialog
	questionForm widgets.QuestionForm
	yoloDlg      widgets.YoloDialog
	textOverlay  widgets.TextOverlay
	filePicker   widgets.FilePicker
	refStore     *referenceStore

	win     *window.Window
	overlay *window.Window

	// Compositing engine
	compositor    *compositor.Compositor
	windowManager *wm.WindowManager
	renderCtx     *widget.RenderContext
	cachedOutput  string // last rendered ANSI output for View() throttling during drag
	dirty         bool   // true when view content has changed and needs re-rendering

	// File tree sidebar
	treeModel            *filetree.FileTreeModel
	treeRenderer         *filetree.TreeRenderer
	treeVisible          bool // whether sidebar is shown (Ctrl+B toggles)
	treeWidth            int  // current sidebar width in cells
	treeResizing         bool // true when user is dragging the divider
	treeResizeStartX     int  // mouse X when resize started
	treeResizeStartWidth int  // width when resize started
	treeFocused          bool // whether the file tree sidebar receives keyboard input
	treeYOffset          int  // screen Y where tree sidebar starts (title bar line), computed in buildMainView

	blocking      bool
	yolo          bool
	tabBarFocused bool

	activeAgentView       string
	activeEditorView      string
	editor                *editor.Editor  // pointer to the active tab's editor; nil if no editor tab
	editorTabs            map[string]*EditorTab // all open editor tabs keyed by absolute path
	agentLabels           map[string]string
	agentModels           map[string]string
	agentTabSnapshotCache string

	sessionInputChans      map[string]chan<- provider.UserInput
	sessionBtwChans        map[string]chan<- provider.UserInput
	sessionCancelChans     map[string]chan<- struct{}
	sessionBlockResolveCh  map[string]chan string
	blockedSessionID       string
	subAgentParents        map[string]string // sub-agent ID → parent orchestrator session ID

	OnQuit              func()
	OnExitMessage       func()
	OnNewSession        func() *NewSessionResult
	OnSettingsSave      func(ttypes.Settings) error
	OnConfirmResult     func(ttypes.ConfirmResult)
	OnQuestionResult    func(ttypes.QuestionResult)
	OnYoloCmd           func(bool)
	OnUndoCmd           func(string)
	OnResumeSession     func(string)
	OnCancelSubAgent    func(agentID string)
	OnHardCancel        func()
	OnOrchestratorReset func(orchID string)
	FetchModelsFn       func(string, string, string) ([]ttypes.ModelEntry, error)
	GetProviderConfigFn func(string) ttypes.ProviderConfig
	ProviderPresets     []ttypes.ProviderPreset

	Quit           bool
	lastCancelTime time.Time
	modelName      string
	username       string
	startTime      time.Time
	chatAreaHeight int

	spinFrame int
	history   []string

	tokenPrompt, tokenCompletion, tokenCached int
	sessionCost                               float64
	contextWindow, contextUsed                int
	tokenEstimated                            bool

	lastSettings ttypes.Settings

	formHint string

	logger Logger

	// Launcher state (replaces old session picker)
	launcher            *LauncherState
	onDeleteSession     func(string)
	onListSessions      func() []sessiondb.SessionMeta
	lastInteractionTime time.Time

	commandPaletteModel *commandPaletteModel

	// Per-agent settings context
	settingsCategory string        // "" = global, "main", "researcher", "crafter", "aeron" or agent ID
	agentCategories  map[string]string // agentID → category name

	diagPopupID string // ID of the diagnostic hover popup window

	lspManager *LSPManager

	composer *widgets.Composer

	// LSP interaction state.
	renameArmed   bool // F2 pressed; the next input is the new symbol name
	outlineOpen   bool // outline panel visible
	outlineRows   []outlineRow
	outlineCursor int
	referencesID  string // ID of the references results popup, "" when closed
	refRows       []refRow
	refCursor     int
	completionID  string // ID of the completion popup
	completionItems []editor.CompletionItem
	completionCursor int

	// dirtyPromptPath holds the path of a tab awaiting confirmation
	// to discard unsaved changes. When non-empty, a small modal
	// "dirty close" dialog is open and keyboard input is routed
	// through dirtyPromptKey().
	dirtyPromptPath string
	dirtyPromptFocusYes bool

	// Find/Replace popup state. The popup lives in the windowManager
	// under ID "find-replace"; the App owns the state struct so
	// callbacks can update it from keyboard input.
	findReplace *widgets.FindReplaceState

	// lastContextMenu is the most recent context menu state. Kept
	// so that keyboard navigation (Up/Down/Enter) can update the
	// cursor position; the actions in the menu items hold their
	// own closures so we don't need to re-build the menu on every
	// keypress.
	lastContextMenu *widgets.ContextMenuState

	// treePrompt drives the small name-input popup used by the
	// file tree's New / Rename actions. The popup lives in the
	// windowManager under ID "tree-prompt"; the App owns the
	// state struct so callbacks can update Value/Cursor.
	treePrompt *widgets.TextPromptState

	// treeDeletePending remembers the path the user is about to
	// delete. The confirm popup lives in the windowManager under
	// "tree-delete-confirm" and reads this on submission.
	treeDeletePending string
}

func (m *App) AddOrchestrator(orchID string, inputCh chan<- provider.UserInput, btwCh chan<- provider.UserInput, cancelCh chan<- struct{}, blockResolveCh chan string) {
	m.launcher = nil
	m.sessionInputChans[orchID] = inputCh
	m.sessionBtwChans[orchID] = btwCh
	m.sessionCancelChans[orchID] = cancelCh
	m.sessionBlockResolveCh[orchID] = blockResolveCh

	ml := widgets.NewMessageList(m.theme)
	ml.SetModelName(m.modelName)
	ml.SetUsername(m.username)
	ml.SetSubPaneContentFn(m.renderSubPaneContent)

	sp := m.paneMgr.GetOrCreate(orchID, PaneAgent, orchID, m.theme)
	sp.MsgList = ml
	sp.MsgList.SetModelName(m.modelName)
	sp.MsgList.SetUsername(m.username)

	agent := NewAgentState(orchID, ml, m.logger)
	m.agents[orchID] = agent
	m.activeAgentView = orchID
	m.syncAgentWindowTabs()
}

func (m *App) RemoveOrchestrator(orchID string) {
	m.cleanupAgent(orchID)
}

// cleanupAgent removes all state for a given agent orchestrator or sub-agent.
func (m *App) cleanupAgent(agentID string) {
	delete(m.sessionInputChans, agentID)
	delete(m.sessionBtwChans, agentID)
	delete(m.sessionCancelChans, agentID)
	delete(m.sessionBlockResolveCh, agentID)
	delete(m.agents, agentID)
	delete(m.agentLabels, agentID)
	delete(m.agentModels, agentID)
	delete(m.agentCategories, agentID)
	for id, parent := range m.subAgentParents {
		if parent == agentID {
			delete(m.subAgentParents, id)
		}
	}
	delete(m.subAgentParents, agentID)
	m.paneMgr.Remove(agentID)
	if m.statusbar != nil {
		m.statusbar.ClearAgentState(agentID)
	}
	if m.activeAgentView == agentID {
		m.activeAgentView = ""
	}
}

func (m *App) activeMsgList() *widgets.MessageList {
	if m.activeAgentView != "" {
		if agent, ok := m.agents[m.activeAgentView]; ok && agent.MsgList != nil {
			return agent.MsgList
		}
	}
	return nil
}

type NewSessionResult struct {
	OrchID         string
	InputCh        chan<- provider.UserInput
	BtwCh          chan<- provider.UserInput
	CancelCh       chan<- struct{}
	BlockResolveCh chan string
}

type (
	tickMsg               = ttypes.TickMsg
	errorMsg              = ttypes.ErrorMsg
	statusMsg             = ttypes.StatusMsg
	agentMsg              = ttypes.AgentMsg
	openSettingsMsg       = ttypes.OpenSettingsMsg
	fetchModelsMsg        = ttypes.FetchModelsMsg
	saveSettingsMsg       = ttypes.SaveSettingsMsg
	updateModelMsg        = ttypes.UpdateModelMsg
	selectionTickMsg      = ttypes.SelectionTickMsg
	settingsSaveResultMsg = ttypes.SettingsSaveResultMsg
)

func NewApp(modelName string, username string, initial ttypes.Settings, onSave func(ttypes.Settings) error, fetchFn func(providerID, baseURL, apiKey string) tea.Cmd, getProviderConfigFn func(string) ttypes.ProviderConfig, lg Logger, sessions []sessiondb.SessionMeta, sessionID, sessionDir string, db *sql.DB) *App {
	th := theme.DefaultDark()
	im := input.NewModel(th)
	sb := widgets.NewStatusBar(th)
	sm := settings.NewModel(th, initial, nil, getProviderConfigFn, fetchFn)
	cd := widgets.NewConfirmDialog(th)
	qd := widgets.NewQuestionForm(th)
	yd := widgets.NewYoloDialog(th)
	to := widgets.NewTextOverlay(th)
	fp := widgets.NewFilePicker(th)
	refs := newReferenceStore(sessionID, sessionDir, db)

	rendering.SetInvalidUTF8Logger(func(tag, msg string, pairs ...any) { lg.Warn(tag, msg, pairs...) })

	pm := NewPaneManager()

	w := window.New(th)
	ov := window.New(th)

	comp := compositor.NewCompositor(80, 24)
	wmgr := wm.NewWindowManager()
	rctx := widget.NewRenderContext(th)

	wsDir, _ := os.Getwd()
	if wsDir == "" {
		wsDir = sessionDir
	}
	treeModel := filetree.NewFileTreeModel(wsDir)
	treeRenderer := filetree.NewTreeRenderer(treeModel, treeModel.IconResolver())
	treeRenderer.ApplyTheme(th)

	launcher := NewLauncherState(sessions)

	ed := editor.New(th)
	lspMgr := NewLSPManager(lg)
	composerWidget := widgets.NewComposer(th)

	app := &App{
		theme:                th,
		Mode:                 ModeChat,
		agents:               make(map[string]*AgentState),
		paneMgr:              pm,
		win:                  w,
		overlay:              ov,
		compositor:           comp,
		windowManager:        wmgr,
		composer:             composerWidget,
		renderCtx:            rctx,
		input:                im,
		statusbar:            sb,
		settings:             sm,
		confirm:              cd,
		questionForm:         qd,
		yoloDlg:              yd,
		textOverlay:          to,
		filePicker:           fp,
		refStore:             refs,
		editor:               ed,
		editorTabs:           make(map[string]*EditorTab),
		agentLabels:          make(map[string]string),
		agentModels:          make(map[string]string),
		agentCategories:      make(map[string]string),
		sessionInputChans:    make(map[string]chan<- provider.UserInput),
		sessionBtwChans:      make(map[string]chan<- provider.UserInput),
		sessionCancelChans:   make(map[string]chan<- struct{}),
		sessionBlockResolveCh: make(map[string]chan string),
		subAgentParents:       make(map[string]string),
		modelName:            modelName,
		username:             username,
		startTime:            time.Now(),
		lastCancelTime:       time.Now(),
		lastInteractionTime:  time.Now(),
		lastSettings:         initial,
		OnSettingsSave:       onSave,
		logger:               lg,
		launcher:             launcher,
		lspManager:           lspMgr,
		treeModel:            treeModel,
		treeRenderer:         treeRenderer,
		treeVisible:          true,
		treeWidth:            th.Tree.Config.DefaultWidth,
		treeYOffset:          3,
	}

	app.input.SetReferenceStore(refs)

	return app
}

func (m *App) renderSubPaneContent(paneID string, width, maxH, spinFrame int) []string {
	sp := m.paneMgr.Get(paneID)
	if sp == nil {
		return []string{"", "● unknown pane"}
	}
	if agent, ok := m.agents[paneID]; ok {
		sp.MsgList.SetStreaming(agent.Streaming)
		sp.MsgList.SetActiveTyping(agent.ComputeActiveTyping())
	}
	result := sp.Render(width, maxH, spinFrame)
	return result.Lines
}

func (m *App) completeSubAgent(agentID string) {
	parentID := m.subAgentParents[agentID]
	if parentID == "" {
		return
	}
	parentAgent := m.agents[parentID]

	agent, agentOK := m.agents[agentID]
	if agentOK && agent.Completed {
		return
	}

	title := m.agentTabTitle(agentID)
	if tab := m.win.TabByTitle(title); tab != nil {
		wasActive := m.win.IndexOf(tab.ID) == m.win.ActiveIndex()
		m.win.CloseTabByID(tab.ID)
		if wasActive && m.activeAgentView == agentID {
			m.activeAgentView = ""
			if parentAgent != nil && parentAgent.MsgList != nil {
				parentAgent.MsgList.InvalidateAllMessages()
			}
		}
	}

	if sp := m.paneMgr.Get(agentID); sp != nil && !sp.Done {
		sp.Collapse()
		if parentAgent != nil && parentAgent.MsgList != nil {
			parentAgent.MsgList.InvalidateSubPane(agentID)
		}
	}

	if agentOK {
		agent.Completed = true
		agent.Streaming = false
		agent.Thinking = false
		if agent.Status == "" || agent.Status == "running" {
			agent.Status = "completed"
		}
	}

	delete(m.agentCategories, agentID)
	m.statusbar.ClearAgentState(agentID)

	if !m.win.IsOpen() {
		m.setMode(ModeChat)
	} else {
		m.syncOverlayMode()
	}
}

func (m *App) anyActive() bool {
	for _, a := range m.agents {
		if !a.Completed && a.IsActive() {
			return true
		}
	}
	for _, sp := range m.paneMgr.panes {
		if !sp.Done {
			return true
		}
	}
	return false
}

func (m *App) SetProviderPresets(presets []ttypes.ProviderPreset) {
	m.ProviderPresets = presets
	m.settings.SetPresets(presets)
}

func (m *App) SetHistory(history []string) {
	m.history = history
	m.input.SetHistory(history)
}

func (m *App) setMode(newMode Mode) {
	oldMode := m.Mode
	if m.logger != nil {
		m.logger.Debug("UI", "mode_switch", "from", modeName(oldMode), "to", modeName(newMode))
	}
	m.Mode = newMode
	if ml := m.activeMsgList(); ml != nil {
		ml.ClearSelection()
		ml.InvalidateAllMessages()
	}
	switch newMode {
	case ModeSettings:
		m.input.ClearInput()
		m.overlay.OpenTab("Settings", "", true)
	case ModeConfirm:
		m.overlay.OpenTab("Confirm", "", true)
	case ModeQuestion:
		m.overlay.OpenTab("Question", "", true)
	case ModeYolo:
		m.overlay.OpenTab("YOLO", "", true)
	case ModeTextOverlay:
		m.openTextOverlayWindow()
	case ModeFilePicker:
		m.openFilePickerWindow()
	case ModeChat:
		m.formHint = ""
		if m.windowManager != nil {
			m.windowManager.RemoveWindow("textoverlay")
			m.windowManager.RemoveWindow("filepicker")
		}
	}
}

func modeName(m Mode) string {
	switch m {
	case ModeChat:
		return "chat"
	case ModeSettings:
		return "settings"
	case ModeConfirm:
		return "confirm"
	case ModeQuestion:
		return "question"
	case ModeYolo:
		return "yolo"
	case ModeTextOverlay:
		return "text-overlay"
	case ModeFilePicker:
		return "file-picker"
	default:
		return "unknown"
	}
}

func (m *App) History() []string {
	return m.history
}

func (m *App) SetDeleteSessionFn(fn func(string)) {
	m.onDeleteSession = fn
}

func (m *App) SetListSessionsFn(fn func() []sessiondb.SessionMeta) {
	m.onListSessions = fn
}

func (m *App) SetReferences(refs []sessiondb.Reference) {
	if m.refStore == nil {
		return
	}
	for _, ref := range refs {
		m.refStore.AddExisting(ref)
	}
	m.input.SetReferenceStore(m.refStore)
}

func (m *App) activeMessageList() *widgets.MessageList {
	activeML := m.activeMsgList()
	if activeML != nil {
		return activeML
	}
	// Fallback: use any available agent's MsgList
	for _, a := range m.agents {
		return a.MsgList
	}
	return nil
}

func (m *App) agentForView() *AgentState {
	if m.activeAgentView == "" {
		for _, a := range m.agents {
			return a
		}
		return nil
	}
	return m.agents[m.activeAgentView]
}

func (m *App) activeAgentID() string {
	return m.activeAgentView
}

func (m *App) switchToAgentView(agentID string) {
	prev := m.activeAgentView
	if agentID == "" {
		m.activeAgentView = ""
	} else if a, ok := m.agents[agentID]; ok && !a.Completed {
		m.activeAgentView = agentID
	}

	if prev != m.activeAgentView {
		if prev != "" {
			if agent, ok := m.agents[prev]; ok && agent.MsgList != nil {
				agent.MsgList.InvalidateAllMessages()
			}
		}
		if m.activeAgentView != "" {
			if agent, ok := m.agents[m.activeAgentView]; ok && agent.MsgList != nil {
				agent.MsgList.InvalidateAllMessages()
			}
		}
	}

	m.syncAgentWindowTabs()
}

// agentTabTitle returns the window tab title for an agent.
// Uses the full agentID to guarantee uniqueness (no truncation).
// Human-readable display names are set via DisplayTitle on the tab.
func (m *App) agentTabTitle(agentID string) string {
	if agentID == "" {
		return "agent:main"
	}
	return "agent:" + agentID
}

// isAgentTabTitle checks if a window tab title is an agent tab.
func (m *App) isAgentTabTitle(title string) bool {
	return len(title) > 6 && title[:6] == "agent:"
}

// agentIDFromTabTitle extracts the agent ID from a window tab title.
// Since tab titles now use the full agentID, parsing is direct.
func (m *App) agentIDFromTabTitle(title string) string {
	if !m.isAgentTabTitle(title) {
		return ""
	}
	return title[6:]
}

// syncAgentWindowTabs creates/removes window tabs to match the current agent set.
// Every agent gets its own tab.
// Tabs are always shown when at least one agent or editor is open.
func (m *App) syncAgentWindowTabs() {
	// Don't switch away from editor tab
	if m.activeEditorView != "" {
		m.purgeStaleAgentTabs()
		return
	}

	// If no agents exist, purge agent tabs and return
	if len(m.agents) == 0 {
		m.purgeAgentTabs()
		m.agentTabSnapshotCache = ""
		return
	}

	// Determine active agent ID
	activeID := m.activeAgentView

	// Create tabs for sub-agents that have panes
	for _, id := range m.paneMgr.paneOrder {
		a, ok := m.agents[id]
		if !ok || a.Completed {
			continue
		}
		title := m.agentTabTitle(id)
		if m.win.TabByTitle(title) == nil {
			m.win.OpenTab(title, "", true)
		}
		if tab := m.win.TabByTitle(title); tab != nil {
			newIcon := m.agentTabIcon(id)
			if tab.Icon != newIcon {
				m.win.SetTabIconByIndex(m.win.IndexOf(tab.ID), newIcon)
			}
			displayLabel := id
			if lbl, ok := m.agentLabels[id]; ok && lbl != "" {
				displayLabel = lbl
			}
			if tab.DisplayTitle != displayLabel {
				m.win.SetTabDisplayTitleByIndex(m.win.IndexOf(tab.ID), displayLabel)
			}
		}
	}

	// Create tabs for orchestrator agents (top-level agents without parent IDs)
	for id, a := range m.agents {
		if a.Completed {
			continue
		}
		if _, isSubAgent := m.subAgentParents[id]; isSubAgent {
			continue
		}
		title := m.agentTabTitle(id)
		if m.win.TabByTitle(title) == nil {
			m.win.OpenTab(title, "", true)
		}
		if tab := m.win.TabByTitle(title); tab != nil {
			newIcon := m.agentTabIcon(id)
			if tab.Icon != newIcon {
				m.win.SetTabIconByIndex(m.win.IndexOf(tab.ID), newIcon)
			}
			displayLabel := id
			if lbl, ok := m.agentLabels[id]; ok && lbl != "" {
				displayLabel = lbl
			}
			if tab.DisplayTitle != displayLabel {
				m.win.SetTabDisplayTitleByIndex(m.win.IndexOf(tab.ID), displayLabel)
			}
		}
	}

	// Activate the correct tab
	if activeID != "" {
		activeTitle := m.agentTabTitle(activeID)
		if tab := m.win.TabByTitle(activeTitle); tab != nil {
			targetIdx := m.win.IndexOf(tab.ID)
			if m.win.ActiveIndex() != targetIdx {
				m.win.SwitchTo(targetIdx)
			}
		}
	}

	m.purgeStaleAgentTabs()
}

func (m *App) agentTabSnapshot() string {
	var b strings.Builder
	for _, id := range m.paneMgr.paneOrder {
		a, ok := m.agents[id]
		if !ok || a.Completed {
			continue
		}
		b.WriteString(id)
		b.WriteByte('|')
		b.WriteString(a.Status)
		b.WriteByte('|')
		if lbl := m.agentLabels[id]; lbl != "" {
			b.WriteString(lbl)
		}
		b.WriteByte('|')
	}
	return b.String()
}

func (m *App) ensureAgentTabActive() {
	activeID := m.activeAgentView
	if activeID == "" {
		return
	}
	activeTitle := m.agentTabTitle(activeID)
	if tab := m.win.TabByTitle(activeTitle); tab != nil {
		targetIdx := m.win.IndexOf(tab.ID)
		if targetIdx >= 0 && m.win.ActiveIndex() != targetIdx {
			m.win.SwitchTo(targetIdx)
		}
	}
}

// agentTabIcon returns the status icon for an agent's window tab.
func (m *App) agentTabIcon(id string) string {
	if id == "" {
		return "■"
	}
	agent, ok := m.agents[id]
	if !ok || agent.Status == "" {
		return "●"
	}
	status := agent.Status
	switch {
	case status == "running":
		return "●"
	case status == "completed":
		return "✓"
	case len(status) > 6 && status[:6] == "failed":
		return "✗"
	case status == "cancelled":
		return "!"
	default:
		return "■"
	}
}

// purgeAgentTabs closes all agent window tabs in a single pass.
func (m *App) purgeAgentTabs() {
	for i := m.win.TabCount() - 1; i >= 0; i-- {
		tabs := m.win.Tabs()
		if i >= len(tabs) {
			continue
		}
		if m.isAgentTabTitle(tabs[i].Title) {
			m.win.CloseTabByID(tabs[i].ID)
		}
	}
}

// purgeStaleAgentTabs closes agent tabs for agents that no longer exist
// or have completed.
func (m *App) purgeStaleAgentTabs() {
	for i := m.win.TabCount() - 1; i >= 0; i-- {
		tabs := m.win.Tabs()
		if i >= len(tabs) {
			continue
		}
		if m.isAgentTabTitle(tabs[i].Title) {
			agentID := m.agentIDFromTabTitle(tabs[i].Title)
			a, ok := m.agents[agentID]
			if !ok || a.Completed {
				m.win.CloseTabByID(tabs[i].ID)
			}
		}
	}
}

// closeAllOverlayTabs closes all overlay tabs and returns to chat mode.
func (m *App) closeAllOverlayTabs() {
	m.overlay.CloseAll()
	if m.Mode != ModeChat {
		m.Mode = ModeChat
		m.formHint = ""
		m.input.ResetPrompt()
		m.input.ClearInput()
	}
}

func (m *App) syncOverlayMode() {
	if !m.overlay.IsOpen() {
		if m.Mode != ModeChat {
			m.Mode = ModeChat
			m.formHint = ""
			m.input.ResetPrompt()
			m.input.ClearInput()
		}
		return
	}
	activeTab := m.overlay.ActiveTab()
	if activeTab == nil {
		if m.Mode != ModeChat {
			m.Mode = ModeChat
			m.formHint = ""
			m.input.ResetPrompt()
			m.input.ClearInput()
		}
		return
	}
	switch {
	case activeTab.Title == "Settings":
		m.Mode = ModeSettings
	case activeTab.Title == "Confirm":
		m.Mode = ModeConfirm
	case activeTab.Title == "YOLO":
		m.Mode = ModeYolo
	case activeTab.Title == "Question":
		m.Mode = ModeQuestion
	default:
		m.Mode = ModeChat
		m.formHint = ""
	}
}

func (m *App) editorTabTitle(path string) string {
	return "editor:" + path
}

func (m *App) isEditorTabTitle(title string) bool {
	return len(title) > 7 && title[:7] == "editor:"
}

func (m *App) editorPathFromTabTitle(title string) string {
	if !m.isEditorTabTitle(title) {
		return ""
	}
	return title[7:]
}

func (m *App) openEditorTab(path string) (tea.Cmd, error) {
	tabTitle := m.editorTabTitle(path)

	// If the tab is already open, just activate it. Crucially, do NOT
	// reload the file — switching tabs must preserve cursor, scroll,
	// undo, and unsaved edits.
	if tab, ok := m.editorTabs[path]; ok && tab.Editor.IsOpen() {
		if m.win.TabByTitle(tabTitle) == nil {
			// Tab exists in our map but the window tab was lost
			// (e.g. window tabs were reset). Re-create it.
			m.win.OpenTab(tabTitle, "✎", true)
		}
		if winTab := m.win.TabByTitle(tabTitle); winTab != nil {
			m.win.SwitchTo(m.win.IndexOf(winTab.ID))
			m.handleTabSwitch()
		}
		return nil, nil
	}

	// Create a fresh Editor for this tab. Per-tab state lives inside
	// the Editor (cursor, scroll, undo, selection, buffer, chroma
	// cache, diagnostics, search state). Switching to another tab and
	// back will give back the same Editor instance with all state
	// intact.
	ed := editor.New(m.theme)
	if err := ed.Open(path); err != nil {
		return nil, err
	}

	// Wire content changes to LSP manager.
	ed.SetOnContentChanged(func(fp, text string) {
		lspCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.lspManager.UpdateFile(lspCtx, fp, text)
	})
	// Apply format-on-save preference from user settings. Defaults to off.
	if m.lastSettings.Editor.FormatOnSave {
		ed.SetFormatOnSave(true)
	} else {
		ed.SetFormatOnSave(false)
	}
	// Open the file on the LSP server.
	lspCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.lspManager.OpenFile(lspCtx, path, ed.Value())

	// Apply cached diagnostics immediately so the editor is never empty
	// while waiting for the server to respond.
	if cached := m.lspManager.FileDiagnostics(path); len(cached) > 0 {
		ed.SetDiagnostics(cached)
	}

	displayTitle := ed.Title()

	// Store the tab and point m.editor at it.
	m.editorTabs[path] = newEditorTab(path, ed)
	m.editor = ed
	m.activeEditorView = path

	if m.win.TabByTitle(tabTitle) == nil {
		m.win.OpenTab(tabTitle, "✎", true)
	}
	if winTab := m.win.TabByTitle(tabTitle); winTab != nil {
		m.win.SetTabDisplayTitleByIndex(m.win.IndexOf(winTab.ID), displayTitle)
		m.win.SwitchTo(m.win.IndexOf(winTab.ID))
		m.handleTabSwitch()
	}

	return ed.BlinkCmd(), nil
}

// jumpToDiagnostic opens the file containing the given diagnostic
// (if not already open), positions the cursor on it, highlights the
// range, scrolls it into view, and refreshes the hover tooltip. The
// caller is responsible for choosing the diagnostic.
func (m *App) jumpToDiagnostic(filePath string, d editor.LSPDiagnostic) {
	if filePath == "" {
		return
	}
	// Open or activate the file. openEditorTab is a no-op if the
	// tab is already the active one.
	if _, err := m.openEditorTab(filePath); err != nil {
		m.statusbar.SetError("Cannot open " + filePath)
		return
	}
	if m.editor == nil {
		return
	}
	m.editor.SetCursorPosition(d.Line, d.Col)
	m.editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.editor.ScrollToCursor()
	layout := m.computeLayout()
	m.editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.dirty = true
}

// gotoFirstDiagnostic opens the first error diagnostic in the
// project (sorted by file path, then by line/col) and jumps to it.
// If a file is already open and has errors, that file's first error
// wins. If the project has no errors, shows a status bar message.
func (m *App) gotoFirstDiagnostic() {
	if m.lspManager == nil {
		m.statusbar.SetInfo("LSP not configured")
		return
	}
	all := m.lspManager.AllDiagnostics()
	if len(all) == 0 {
		m.statusbar.SetInfo("No errors in project")
		return
	}

	// Prefer an error in the currently-active file (if any). The
	// user's current focus is more important than file-path order.
	var paths []string
	if active := m.activeEditorPath(); active != "" {
		if diags, ok := all[active]; ok {
			if e := firstErrorOf(diags); e != nil {
				m.jumpToDiagnostic(active, *e)
				m.statusbar.SetInfo(fmt.Sprintf("Jumped to %s:%d",
					filepath.Base(active), e.Line+1))
				return
			}
		}
		paths = append(paths, active)
	}
	// Collect remaining paths in sorted order so the navigation is
	// deterministic across runs.
	for p := range all {
		if p == m.activeEditorPath() {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		e := firstErrorOf(all[p])
		if e == nil {
			continue
		}
		m.jumpToDiagnostic(p, *e)
		m.statusbar.SetInfo(fmt.Sprintf("Jumped to %s:%d",
			filepath.Base(p), e.Line+1))
		return
	}

	m.statusbar.SetInfo("No errors in project")
}

// firstErrorOf returns the first error-severity diagnostic in the
// given list (sorted by line then col, as LSP publishes them). The
// returned pointer aliases the input slice — safe to dereference
// before the next mutation.
func firstErrorOf(diags []editor.LSPDiagnostic) *editor.LSPDiagnostic {
	for i := range diags {
		if diags[i].Severity == editor.DiagError {
			return &diags[i]
		}
	}
	return nil
}

// jumpToNextError moves the cursor to the next error diagnostic in
// the currently-open file (strictly after the current cursor). If
// there is no editor, no file is open, or no further errors, shows
// a status bar message and returns false. Returns true on success.
func (m *App) jumpToNextError() bool {
	if m.editor == nil || !m.editor.IsOpen() {
		m.statusbar.SetInfo("No file open")
		return false
	}
	d := m.editor.NextError(m.editor.CursorLine(), m.editor.CursorCol())
	if d == nil {
		// Wrap around to the first error so F8 cycles through
		// every error like VSCode's command.
		d = m.editor.FirstError()
		if d == nil {
			m.statusbar.SetInfo("No errors in file")
			return false
		}
	}
	m.editor.SetCursorPosition(d.Line, d.Col)
	m.editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.editor.ScrollToCursor()
	layout := m.computeLayout()
	m.editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.statusbar.SetInfo(fmt.Sprintf("Error at line %d: %s", d.Line+1, d.Message))
	m.dirty = true
	return true
}

// jumpToPrevError moves the cursor to the previous error diagnostic
// in the currently-open file (strictly before the current cursor).
// Mirrors jumpToNextError and wraps around to the last error.
func (m *App) jumpToPrevError() bool {
	if m.editor == nil || !m.editor.IsOpen() {
		m.statusbar.SetInfo("No file open")
		return false
	}
	d := m.editor.PrevError(m.editor.CursorLine(), m.editor.CursorCol())
	if d == nil {
		// Wrap to the last error in the file.
		diags := m.editor.Diagnostics()
		for i := len(diags) - 1; i >= 0; i-- {
			if diags[i].Severity == editor.DiagError {
				d = &diags[i]
				break
			}
		}
		if d == nil {
			m.statusbar.SetInfo("No errors in file")
			return false
		}
	}
	m.editor.SetCursorPosition(d.Line, d.Col)
	m.editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.editor.ScrollToCursor()
	layout := m.computeLayout()
	m.editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.statusbar.SetInfo(fmt.Sprintf("Error at line %d: %s", d.Line+1, d.Message))
	m.dirty = true
	return true
}

func (m *App) closeEditorTab() {
	if m.activeEditorPath() == "" {
		return
	}
	path := m.activeEditorPath()

	// If the tab has unsaved edits, prompt before destroying the
	// buffer. The popup is modal; on confirm we re-enter with a
	// force flag.
	if tab, ok := m.editorTabs[path]; ok && tab.Editor != nil && tab.Editor.Modified() {
		m.showDirtyClosePrompt(path)
		return
	}

	m.closeEditorTabForce(path)
}

// closeEditorTabForce unconditionally closes the given editor tab.
// Used both for the normal close path and after the user confirms a
// close-with-unsaved-changes prompt.
func (m *App) closeEditorTabForce(path string) {
	tabTitle := m.editorTabTitle(path)
	if tab := m.win.TabByTitle(tabTitle); tab != nil {
		m.win.CloseTabByID(tab.ID)
	}

	tab, ok := m.editorTabs[path]
	if ok {
		lspCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.lspManager.CloseFile(lspCtx, path)
		tab.Editor.Close()
		delete(m.editorTabs, path)
	}

	// Pick a new active editor tab if any remain. The window tab
	// switcher has already advanced ActiveIndex; the next call to
	// handleTabSwitch() will look up the new active path in the map.
	m.handleTabSwitch()

	if len(m.agents) == 0 && m.activeEditorView == "" {
		if m.onListSessions != nil {
			m.launcher = NewLauncherState(m.onListSessions())
			m.lastInteractionTime = time.Now()
		}
	}
}

// openFindReplace opens the find/replace floating popup above the
// active editor. If mode is "replace", the replace field is shown.
// If the popup is already open, the mode flag is updated and the
// popup re-rendered.
func (m *App) openFindReplace(replaceMode bool) {
	if m.editor == nil || !m.editor.IsOpen() {
		return
	}
	if m.findReplace == nil {
		m.findReplace = &widgets.FindReplaceState{}
	}
	m.findReplace.ReplaceMode = replaceMode
	// Pull current search state from the editor so the popup opens
	// with the existing query if the user already had one typed.
	widgets.FindReplaceFromEditor(m.findReplace, m.editor)
	m.findReplace.FocusedField = 0
	if m.findReplace.Cursor < 0 {
		m.findReplace.Cursor = len(m.findReplace.Query)
	}

	// Wire callbacks to mutate the editor.
	fr := m.findReplace
	fr.OnChange = func() {
		widgets.FindReplaceToEditor(fr, m.editor)
		fr.MatchCount = len(m.editor.Search().Matches)
		fr.CurrentIdx = m.editor.Search().CurrentIdx
		m.refreshFindReplacePopup()
	}
	fr.OnNext = func() {
		if n, ok := m.editor.NextMatch(); ok {
			fr.MatchCount = n
			fr.CurrentIdx = m.editor.Search().CurrentIdx
			m.refreshFindReplacePopup()
		}
	}
	fr.OnPrev = func() {
		if n, ok := m.editor.PrevMatch(); ok {
			fr.MatchCount = n
			fr.CurrentIdx = m.editor.Search().CurrentIdx
			m.refreshFindReplacePopup()
		}
	}
	fr.OnReplace = func() {
		if n := m.editor.ReplaceCurrent(); n >= 0 {
			fr.MatchCount = n
			fr.CurrentIdx = m.editor.Search().CurrentIdx
			m.refreshFindReplacePopup()
		}
	}
	fr.OnReplaceAll = func() {
		count := m.editor.ReplaceAll()
		m.statusbar.SetInfo(fmt.Sprintf("Replaced %d occurrences", count))
		fr.MatchCount = len(m.editor.Search().Matches)
		fr.CurrentIdx = m.editor.Search().CurrentIdx
		m.refreshFindReplacePopup()
	}
	fr.OnClose = func() {
		m.closeFindReplace()
	}

	m.refreshFindReplacePopup()
}

// closeFindReplace closes the find/replace popup. The editor's
// search state is preserved (so re-opening shows the same query),
// but highlights stop rendering because Open is set to false on the
// editor's SearchState.
func (m *App) closeFindReplace() {
	if m.editor != nil {
		m.editor.SetSearchOpen(false)
	}
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("find-replace")
	}
	m.findReplace = nil
	m.dirty = true
}

// refreshFindReplacePopup rebuilds the floating window's content
// lines. Called when the user types, toggles an option, or
// navigates.
func (m *App) refreshFindReplacePopup() {
	if m.windowManager == nil || m.findReplace == nil {
		return
	}
	lines := widgets.FindReplaceLines(m.theme, m.findReplace)
	pop := widgets.NewPopupContent(lines)

	// FindReplaceLines now self-renders the unified popup chrome.
	// Use the popup's own width/height functions.
	w := widgets.FindReplacePopupWidth(m.findReplace)
	h := len(lines)

	if fw := m.windowManager.Window("find-replace"); fw != nil {
		// Just refresh the content. Don't re-center, since the
		// user may have moved it.
		fw.SetSize(w, h)
		fw.SetContent(pop)
		fw.MarkContentDirty()
		return
	}

	// First time: create and position above the active editor.
	layout := m.computeLayout()

	// The chrome is self-rendered. The FloatingWindow is a plain
	// positioned box.
	fw := wm.NewFloatingWindow("find-replace", "", pop, w, h,
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(false), // not modal — user can still click editor
		wm.WithFocusable(true),
	)
	// Position in the top-right of the editor area, with margin.
	x := layout.SidebarW + m.editor.PadL + (layout.EditorW - w) - 2
	if x < layout.SidebarW {
		x = layout.SidebarW + 2
	}
	y := layout.ChatAreaTop + 1
	fw.SetPosition(x, y)
	m.windowManager.AddWindow(fw)
}

// showDirtyClosePrompt pops up a small yes/no dialog asking whether
// to discard unsaved changes for the given tab. Calls
// closeEditorTabForce on Yes, dismisses on No.
func (m *App) showDirtyClosePrompt(path string) {
	if m.windowManager == nil {
		// No floating-window support? Just close and lose the data.
		m.closeEditorTabForce(path)
		return
	}
	tab, ok := m.editorTabs[path]
	if !ok {
		return
	}
	filename := tab.Editor.Title()
	detail := fmt.Sprintf("'%s' has unsaved changes.\nClosing will discard them.", filename)
	// ConfirmPrompt now renders its own border+title (unified design).
	// The FloatingWindow is just a positioned box with no decoration.
	lines := widgets.ConfirmPrompt(m.theme,
		"Unsaved changes",
		"Discard unsaved changes?",
		detail,
		widgets.ConfirmPromptWarning,
		false,
		56,
	)

	const id = "dirty-close-prompt"
	m.windowManager.RemoveWindow(id)
	pop := widgets.NewPopupContent(lines)
	fw := wm.NewFloatingWindow(id, "", pop, 56, len(lines),
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.windowManager.AddWindow(fw)
	m.dirtyPromptPath = path
}

func popLongestLine(lines []string) string {
	longest := ""
	for _, l := range lines {
		// Strip ANSI sequences for the width check.
		plain := stripANSI(l)
		if len(plain) > len(longest) {
			longest = plain
		}
	}
	return longest
}

func stripANSI(s string) string {
	var out strings.Builder
	inEscape := false
	for _, r := range s {
		if r == 0x1b {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// dirtyPromptYes is called when the user confirms closing a dirty
// editor tab. Routes through closeEditorTabForce.
func (m *App) dirtyPromptYes() {
	path := m.dirtyPromptPath
	m.dirtyPromptPath = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("dirty-close-prompt")
	}
	if path != "" {
		m.closeEditorTabForce(path)
	}
}

// dirtyPromptNo is called when the user cancels closing a dirty
// editor tab.
func (m *App) dirtyPromptNo() {
	m.dirtyPromptPath = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("dirty-close-prompt")
	}
}

// handleDirtyPromptKey routes keypresses when the dirty-close dialog
// is open. Updates focus, re-renders the popup, and dispatches
// confirm/cancel.
func (m *App) handleDirtyPromptKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.dirtyPromptPath == "" {
		return nil
	}
	switch msg.Code {
	case tea.KeyLeft, tea.KeyUp:
		m.dirtyPromptFocusYes = true
		m.refreshDirtyPrompt()
		return nil
	case tea.KeyRight, tea.KeyDown:
		m.dirtyPromptFocusYes = false
		m.refreshDirtyPrompt()
		return nil
	case tea.KeyTab:
		// Shift+Tab moves focus left (Yes), plain Tab moves right (No).
		m.dirtyPromptFocusYes = msg.Mod.Contains(tea.ModShift)
		m.refreshDirtyPrompt()
		return nil
	case tea.KeyEnter:
		m.dirtyPromptYes()
		return nil
	case tea.KeyEsc:
		m.dirtyPromptNo()
		return nil
	}
	switch msg.String() {
	case "y", "Y":
		m.dirtyPromptFocusYes = true
		m.dirtyPromptYes()
		return nil
	case "n", "N":
		m.dirtyPromptFocusYes = false
		m.dirtyPromptNo()
		return nil
	}
	return nil
}

// buildEditorContextMenu creates the menu shown on right-click in
// the editor. Items are smart: presence depends on whether the
// cursor is on a diagnostic, on a symbol, on a selection, or just on
// empty text. The menu's actions wire into existing LSP / editing
// infrastructure.
func (m *App) buildEditorContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	if m.editor == nil || !m.editor.IsOpen() {
		return nil
	}
	st := &widgets.ContextMenuState{Title: "Editor"}

	// Cut/Copy/Paste (no section label: the popup title "Editor"
	// already provides context, and the items speak for themselves)
	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		Items: []widgets.ContextMenuItem{
			{Label: "Copy", Detail: "Ctrl+C", Disabled: !m.editor.HasSelection(), Action: func() {
				m.copyEditorSelection()
			}},
			{Label: "Cut", Detail: "Ctrl+X", Disabled: !m.editor.HasSelection(), Action: func() {
				m.cutEditorSelection()
			}},
			{Label: "Paste", Detail: "Ctrl+V", Action: func() {
				m.pasteIntoEditor()
			}},
		},
	})

	// Selection-aware items
	selSection := widgets.ContextMenuSection{SectionLabel: "Selection"}
	selItems := []widgets.ContextMenuItem{}
	if m.editor.HasSelection() {
		selItems = append(selItems, widgets.ContextMenuItem{
			Label: "Format selection", Action: func() {
				// Best-effort: rely on Save+format-on-save for now.
				// This is a placeholder for a future range-format
				// hook.
			},
		})
	}
	selSection.Items = selItems
	if len(selItems) > 0 {
		st.Sections = append(st.Sections, selSection)
	}

	// Diagnostic- and symbol-aware items.
	// Get the line/col under the cursor.
	cx, cy := m.editor.CursorScreenPosition(0, 0)
	_ = cx
	_ = cy
	// Use the click position relative to the editor to find the
	// (line, col) — but for simplicity we just use the current
	// cursor position. Future: translate mouse X/Y to (line, col).
	pos := struct{ Line, Col int }{Line: m.editor.CursorLine(), Col: m.editor.CursorCol()}
	// Use the diagnostics helper on the editor. We don't expose
	// it, but the editor has a diagnosticsAt function via
	// SetDiagnostics/LSPDiagnostic; we just need the existence
	// check.
	diags := m.editor.DiagnosticsAt(pos.Line, pos.Col)
	hasLSP := m.lspManager != nil && m.lspManager.ServerReady()

	lspSection := widgets.ContextMenuSection{SectionLabel: "Code"}
	if hasLSP {
		lspSection.Items = append(lspSection.Items, widgets.ContextMenuItem{
			Label: "Go to definition", Detail: "Alt+.", Action: func() {
				_, _ = m.requestDefinitionCmd(), 0
			},
		})
		lspSection.Items = append(lspSection.Items, widgets.ContextMenuItem{
			Label: "Find references", Detail: "Alt+;", Action: func() {
				_, _ = m.requestReferencesCmd(), 0
			},
		})
		lspSection.Items = append(lspSection.Items, widgets.ContextMenuItem{
			Label: "Rename symbol", Detail: "F2", Action: func() {
				m.startRename()
			},
		})
	}
	if len(diags) > 0 {
		lspSection.Items = append(lspSection.Items, widgets.ContextMenuItem{
			Label: fmt.Sprintf("Show %d diagnostic(s)", len(diags)), Action: func() {
				m.showDiagnosticAt(pos.Line, pos.Col)
			},
		})
	}
	if len(lspSection.Items) > 0 {
		st.Sections = append(st.Sections, lspSection)
	}

	// View actions.
	viewSection := widgets.ContextMenuSection{
		SectionLabel: "View",
		Items: []widgets.ContextMenuItem{
			{Label: "Find", Detail: "Ctrl+F", Action: func() {
				m.openFindReplace(false)
			}},
			{Label: "Find & replace", Detail: "Ctrl+H", Action: func() {
				m.openFindReplace(true)
			}},
			{Label: "Document outline", Detail: "Alt+O", Disabled: !hasLSP, Action: func() {
				_, _ = m.requestDocumentSymbolsCmd(), 0
			}},
		},
	}
	st.Sections = append(st.Sections, viewSection)

	return st
}

// buildSidebarContextMenu creates the menu for right-click on a file
// tree item.
func (m *App) buildSidebarContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	if m.treeModel == nil {
		return nil
	}
	st := &widgets.ContextMenuState{Title: "File"}

	// Get the path under the click.
	path := m.pathUnderMouse(mouse)
	isDir := path != "" && m.treeModel.IsDir(path)

	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		SectionLabel: "Actions",
		Items: []widgets.ContextMenuItem{
			{Label: "Open", Disabled: path == "", Action: func() {
				if path != "" {
					_, _ = m.openFileFromTree(path)
				}
			}},
			{Label: "Reveal in tree", Disabled: path == "", Action: func() {
				if path != "" {
					m.treeModel.ExpandToPath(path)
				}
			}},
		},
	})

	createSection := widgets.ContextMenuSection{SectionLabel: "Create"}
	if isDir {
		createSection.Items = append(createSection.Items,
			widgets.ContextMenuItem{Label: "New file", Action: func() {
				m.startNewFileInTree(path)
			}},
			widgets.ContextMenuItem{Label: "New folder", Action: func() {
				m.startNewFolderInTree(path)
			}},
		)
	}
	if path != "" {
		createSection.Items = append(createSection.Items,
			widgets.ContextMenuItem{Label: "New file (sibling)", Action: func() {
				m.startNewFileInTree(m.treeModel.ParentDir(path))
			}},
			widgets.ContextMenuItem{Label: "New folder (sibling)", Action: func() {
				m.startNewFolderInTree(m.treeModel.ParentDir(path))
			}},
		)
	}
	if len(createSection.Items) > 0 {
		st.Sections = append(st.Sections, createSection)
	}

	if path != "" {
		st.Sections = append(st.Sections, widgets.ContextMenuSection{
			SectionLabel: "Modify",
			Items: []widgets.ContextMenuItem{
				{Label: "Rename", Action: func() {
					m.startRenameInTree(path)
				}},
				{Label: "Delete", Action: func() {
					m.startDeleteInTree(path)
				}},
				{Label: "Copy path", Action: func() {
					m.copyToClipboard(path)
				}},
			},
		})
	}

	return st
}

// buildTabBarContextMenu creates the menu for right-click on a tab.
func (m *App) buildTabBarContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	if m.win == nil || m.win.TabCount() == 0 {
		return nil
	}
	adjustedX := mouse.X
	if m.treeVisible && m.treeModel != nil {
		adjustedX -= m.treeWidth + 1
	}
	adjustedX -= m.theme.Spacing.PadLR
	if adjustedX < 0 {
		adjustedX = 0
	}
	hit := m.win.HitTest(adjustedX, 0)
	if hit.TabIndex < 0 || hit.TabIndex >= m.win.TabCount() {
		return nil
	}
	tabs := m.win.Tabs()
	tab := tabs[hit.TabIndex]

	st := &widgets.ContextMenuState{Title: "Tab"}

	// Get tab-specific info.
	if m.isEditorTabTitle(tab.Title) {
		path := m.editorPathFromTabTitle(tab.Title)
		st.Sections = append(st.Sections, widgets.ContextMenuSection{
			SectionLabel: "Tab",
			Items: []widgets.ContextMenuItem{
				{Label: "Close", Detail: "Ctrl+W", Action: func() {
					m.activeEditorView = path
					_ = m.handleAgentTabClose(hit.TabIndex)
				}},
				{Label: "Close others", Action: func() {
					m.closeAllEditorTabsExcept(path)
				}},
				{Label: "Close to the right", Action: func() {
					m.closeEditorTabsToRightOf(hit.TabIndex)
				}},
				{Label: "Copy path", Action: func() {
					m.copyToClipboard(path)
				}},
				{Label: "Reveal in tree", Action: func() {
					if m.treeModel != nil {
						m.treeModel.ExpandToPath(path)
					}
				}},
			},
		})
	} else if m.isAgentTabTitle(tab.Title) {
		agentID := m.agentIDFromTabTitle(tab.Title)
		st.Sections = append(st.Sections, widgets.ContextMenuSection{
			SectionLabel: "Tab",
			Items: []widgets.ContextMenuItem{
				{Label: "Close", Action: func() {
					_ = m.handleAgentTabClose(hit.TabIndex)
				}},
				{Label: "Cancel agent", Action: func() {
					if m.OnCancelSubAgent != nil {
						go m.OnCancelSubAgent(agentID)
					}
				}},
			},
		})
	}

	return st
}

// buildInputContextMenu creates the menu for the chat input.
func (m *App) buildInputContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	st := &widgets.ContextMenuState{Title: "Input"}
	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		SectionLabel: "Clipboard",
		Items: []widgets.ContextMenuItem{
			{Label: "Paste", Detail: "Ctrl+V", Action: func() {
				m.pasteIntoInput()
			}},
			{Label: "Copy input", Action: func() {
				m.copyToClipboard(m.input.Value())
			}},
			{Label: "Clear input", Action: func() {
				m.input.ClearInput()
			}},
		},
	})
	return st
}

// buildChatContextMenu creates the menu for right-click in the
// message list. (For now: clipboard + a copy-all option.)
func (m *App) buildChatContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	st := &widgets.ContextMenuState{Title: "Chat"}
	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		Items: []widgets.ContextMenuItem{
			{Label: "Copy selection", Action: func() {
				ml := m.activeMsgList()
				if ml != nil {
					text := ml.SelectedText()
					if text != "" {
						m.copyToClipboard(text)
					}
				}
			}},
			{Label: "Copy last assistant", Action: func() {
				ml := m.activeMsgList()
				if ml != nil {
					text := ml.LastAssistantText()
					if text != "" {
						m.copyToClipboard(text)
					}
				}
			}},
			{Label: "Copy last code block", Action: func() {
				ml := m.activeMsgList()
				if ml == nil {
					return
				}
				raw := ml.LastAssistantText()
				lang, code, ok := rendering.ExtractLastCodeBlock(raw)
				if !ok {
					m.statusbar.SetInfo("No code block in last assistant message")
					return
				}
				if lang != "" {
					m.statusbar.SetInfo(fmt.Sprintf("Copied %s code block (%d chars)", lang, len(code)))
				} else {
					m.statusbar.SetInfo(fmt.Sprintf("Copied code block (%d chars)", len(code)))
				}
				if err := clipboard.WriteText(code); err != nil {
					m.statusbar.SetError("clipboard error: " + err.Error())
				}
			}},
		},
	})
	return st
}

// buildStatusBarContextMenu creates the menu for right-click on the
// bottom status bar. Items surface LSP controls, status-line copy,
// and a quick "go to first diagnostic" action.
func (m *App) buildStatusBarContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	st := &widgets.ContextMenuState{Title: "Status Bar"}

	// LSP section. Always shown when an LSPManager exists; items
	// disable themselves when not applicable.
	if m.lspManager != nil {
		var lspItems []widgets.ContextMenuItem

		isConfigured := m.lspManager.IsConfigured()
		serverReady := m.lspManager.ServerReady()
		ins := m.lspManager.ProjectInsights()
		hasErrors := ins.Errors > 0

		lspItems = append(lspItems, widgets.ContextMenuItem{
			Label:    "Restart LSP",
			Detail:   "kill+relaunch",
			Disabled: !isConfigured,
			Action: func() {
				m.StopLSP()
				m.startLSPForWorkspace()
				m.statusbar.SetInfo("LSP restarting…")
			},
		})

		if serverReady {
			lspItems = append(lspItems, widgets.ContextMenuItem{
				Label:  "Stop LSP",
				Detail: "free port",
				Action: func() {
					m.StopLSP()
					m.statusbar.SetInfo("LSP stopped")
				},
			})
		} else if isConfigured {
			// Server was configured but not connected (e.g. failed
			// or still starting). Offer to start it.
			lspItems = append(lspItems, widgets.ContextMenuItem{
				Label: "Start LSP",
				Action: func() {
					m.startLSPForWorkspace()
					m.statusbar.SetInfo("LSP starting…")
				},
			})
		}

		if hasErrors {
			lspItems = append(lspItems, widgets.ContextMenuItem{
				Label:  "Goto first error",
				Detail: "F8",
				Action: func() {
					m.gotoFirstDiagnostic()
				},
			})
		}

		if ins.FilesTracked > 0 {
			summary := fmt.Sprintf("%s · ✕%d ⚠%d ℹ%d · %d files · %d symbols",
				ins.ServerName, ins.Errors, ins.Warnings,
				ins.Info+ins.Hints, ins.FilesAffected, ins.SymbolsCount)
			lspItems = append(lspItems, widgets.ContextMenuItem{
				Label: "Copy diagnostics summary",
				Action: func() {
					m.copyToClipboard(summary)
				},
			})
		}

		if len(lspItems) > 0 {
			st.Sections = append(st.Sections, widgets.ContextMenuSection{
				SectionLabel: "LSP",
				Items:        lspItems,
			})
		}
	}

	// Status section. Always available.
	statusItems := []widgets.ContextMenuItem{
		{
			Label: "Copy status line",
			Action: func() {
				if m.statusbar != nil {
					m.copyToClipboard(m.statusbar.View())
				}
			},
		},
	}
	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		SectionLabel: "Status",
		Items:        statusItems,
	})

	return st
}

// closeContextMenu removes the open context menu, if any.
func (m *App) closeContextMenu() {
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("context-menu")
	}
	m.dirty = true
}

// handleContextMenuKey routes keypresses to the context menu. Up/
// Down navigate items, Enter activates, Esc dismisses.
func (m *App) handleContextMenuKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Window("context-menu")
	if fw == nil {
		return nil
	}
	st := m.lastContextMenu
	if st == nil {
		m.closeContextMenu()
		return nil
	}
	switch msg.Code {
	case tea.KeyEsc:
		m.closeContextMenu()
		return nil
	case tea.KeyEnter:
		st.Activate()
		m.closeContextMenu()
		return nil
	case tea.KeyDown:
		st.Next()
		m.refreshContextMenu()
		return nil
	case tea.KeyUp:
		st.Prev()
		m.refreshContextMenu()
		return nil
	}
	return nil
}

// refreshContextMenu re-renders the context menu popup after the
// cursor moved.
func (m *App) refreshContextMenu() {
	if m.windowManager == nil {
		return
	}
	fw := m.windowManager.Window("context-menu")
	if fw == nil || m.lastContextMenu == nil {
		return
	}
	lines := widgets.ContextMenuLines(m.theme, m.lastContextMenu)
	pop := widgets.NewPopupContent(lines)
	fw.SetContent(pop)
	fw.MarkContentDirty()
	m.dirty = true
}

// handleTreePromptKey routes keypresses to the focused tree name
// input popup (new file/folder, rename). Backspace, arrows, and
// printable characters edit the value; Enter submits; Esc cancels.
func (m *App) handleTreePromptKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.treePrompt == nil || m.windowManager == nil {
		return nil
	}
	st := m.treePrompt
	switch msg.Code {
	case tea.KeyEsc:
		if st.OnCancel != nil {
			st.OnCancel()
		}
		m.closeTreePrompt()
		return nil
	case tea.KeyEnter:
		if st.OnSubmit != nil {
			if err := st.OnSubmit(st.Value); err != nil {
				m.statusbar.SetError(err.Error())
				m.dirty = true
				return nil
			}
		}
		m.closeTreePrompt()
		return nil
	case tea.KeyBackspace:
		if st.Cursor > 0 && len(st.Value) > 0 {
			st.Value = st.Value[:st.Cursor-1] + st.Value[st.Cursor:]
			st.Cursor--
			m.refreshTreePrompt()
		}
		return nil
	case tea.KeyDelete:
		if st.Cursor < len(st.Value) {
			st.Value = st.Value[:st.Cursor] + st.Value[st.Cursor+1:]
			m.refreshTreePrompt()
		}
		return nil
	case tea.KeyLeft:
		if st.Cursor > 0 {
			st.Cursor--
			m.refreshTreePrompt()
		}
		return nil
	case tea.KeyRight:
		if st.Cursor < len(st.Value) {
			st.Cursor++
			m.refreshTreePrompt()
		}
		return nil
	case tea.KeyHome:
		st.Cursor = 0
		m.refreshTreePrompt()
		return nil
	case tea.KeyEnd:
		st.Cursor = len(st.Value)
		m.refreshTreePrompt()
		return nil
	}
	if msg.Text != "" {
		st.Value = st.Value[:st.Cursor] + msg.Text + st.Value[st.Cursor:]
		st.Cursor += len(msg.Text)
		m.refreshTreePrompt()
	}
	return nil
}

func (m *App) closeTreePrompt() {
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-prompt")
	}
	m.treePrompt = nil
	m.dirty = true
}

func (m *App) refreshTreePrompt() {
	if m.windowManager == nil || m.treePrompt == nil {
		return
	}
	fw := m.windowManager.Window("tree-prompt")
	if fw == nil {
		return
	}
	lines := widgets.TextPromptLines(m.theme, m.treePrompt)
	pop := widgets.NewPopupContent(lines)
	fw.SetContent(pop)
	fw.MarkContentDirty()
	m.dirty = true
}

// handleTreeDeleteConfirmKey is the keyboard handler for the
// destructive confirm popup (delete file/folder). ←/↑ = Yes,
// →/↓ = No, Enter = activate focused, Esc = cancel.
func (m *App) handleTreeDeleteConfirmKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Window("tree-delete-confirm")
	if fw == nil {
		return nil
	}
	path := m.treeDeletePending
	switch msg.Code {
	case tea.KeyEsc:
		m.cancelTreeDelete()
		return nil
	case tea.KeyEnter:
		m.confirmTreeDelete()
		return nil
	case tea.KeyLeft, tea.KeyUp:
		m.statusbar.SetInfo("press enter to confirm delete of " + filepath.Base(path))
		return nil
	case tea.KeyRight, tea.KeyDown:
		m.cancelTreeDelete()
		return nil
	}
	return nil
}

func (m *App) confirmTreeDelete() {
	path := m.treeDeletePending
	m.treeDeletePending = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-delete-confirm")
	}
	if path == "" {
		return
	}
	isDir := m.treeModel.IsDir(path)
	var err error
	if isDir {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil {
		m.statusbar.SetError("delete " + filepath.Base(path) + ": " + err.Error())
		m.dirty = true
		return
	}
	m.treeModel.ProcessWatcherEvent(filetree.FileChangeEvent{
		Type: filetree.FileDeleted, Path: path,
	})
	// If the deleted file was open in an editor, close the tab.
	if tab, ok := m.editorTabs[path]; ok {
		m.closeEditorTabForce(tab.Path)
	}
	m.statusbar.SetInfo("Deleted " + filepath.Base(path))
	m.dirty = true
}

func (m *App) cancelTreeDelete() {
	m.treeDeletePending = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-delete-confirm")
	}
	m.dirty = true
}

// showContextMenuAt opens a context menu at the given screen
// position. Used by the Ctrl+. keyboard shortcut.
func (m *App) showContextMenuAt(x, y int) {
	msg := tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight}
	_, _ = m.dispatchMouseRightClick(msg)
}

// pathUnderMouse resolves a file tree path from a screen Y.
func (m *App) pathUnderMouse(mouse tea.Mouse) string {
	if m.treeModel == nil {
		return ""
	}
	visible := m.treeModel.VisibleRows()
	if visible == nil {
		return ""
	}
	row := mouse.Y - m.treeYOffset
	if row < 0 || row >= len(visible) {
		return ""
	}
	return visible[row]
}

// copyEditorSelection copies the active selection to the system
// clipboard. Falls back silently if there's no selection.
func (m *App) copyEditorSelection() {
	if m.editor == nil || !m.editor.HasSelection() {
		return
	}
	m.copyToClipboard(m.editor.SelectedText())
}

// cutEditorSelection copies and deletes the selection.
func (m *App) cutEditorSelection() {
	if m.editor == nil || !m.editor.HasSelection() {
		return
	}
	m.copyToClipboard(m.editor.SelectedText())
	m.editor.DeleteSelection()
	m.dirty = true
}

// pasteIntoEditor pastes from the system clipboard at the cursor.
// Reads the clipboard synchronously; this is fast (sub-10ms) on all
// supported platforms and the clipboard package is mutex-locked.
func (m *App) pasteIntoEditor() {
	if m.editor == nil {
		return
	}
	data := clipboard.Read(clipboard.FmtText)
	if len(data) == 0 {
		m.statusbar.SetError("clipboard is empty or unavailable")
		return
	}
	content := string(data)
	m.editor.HandleEvent(tea.PasteMsg{Content: content})
	m.statusbar.SetInfo(fmt.Sprintf("Pasted %d chars", len(content)))
	m.dirty = true
}

// pasteIntoInput pastes into the chat input. Reads the clipboard
// synchronously and forwards the result through the input's own
// paste handler so large pastes are properly offloaded to refs.
func (m *App) pasteIntoInput() {
	data := clipboard.Read(clipboard.FmtText)
	if len(data) == 0 {
		m.statusbar.SetError("clipboard is empty or unavailable")
		return
	}
	content := string(data)
	canType := !m.blocking
	updated, _, _ := m.input.Update(tea.PasteMsg{Content: content},
		canType, m.blocking, m.chatAreaHeight, m.input.ShowHint())
	m.input = updated
	m.statusbar.SetInfo(fmt.Sprintf("Pasted %d chars into input", len(content)))
	m.dirty = true
}

// copyToClipboard copies a string to the system clipboard via the
// real platform layer (Unix wl-copy/pbcopy/xclip/termux, Windows
// Win32). Shows a status bar message on success or error.
func (m *App) copyToClipboard(s string) {
	if s == "" {
		return
	}
	if err := clipboard.WriteText(s); err != nil {
		m.statusbar.SetError("clipboard error: " + err.Error())
		return
	}
	m.statusbar.SetInfo(fmt.Sprintf("Copied %d chars", len(s)))
}

// showDiagnosticAt opens the diagnostic hover popup for the
// position, if there's a diagnostic.
func (m *App) showDiagnosticAt(line, col int) {
	diags := m.editor.DiagnosticsAt(line, col)
	if len(diags) == 0 {
		return
	}
	// Build inner content: one row per diagnostic message.
	var content []string
	for _, d := range diags {
		content = append(content, d.Message)
	}
	// Wrap in unified chrome.
	lines := widgets.PopupChromeLines(m.theme, widgets.PopupChromeConfig{
		Title:    "Diagnostic",
		TitleIcon: "ℹ",
		Width:    60,
		Focused:  true,
	}, content)
	pop := widgets.NewPopupContent(lines)
	fw := wm.NewFloatingWindow("diag-context", "", pop, 60, len(lines),
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(false),
		wm.WithFocusable(true),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("diag-context")
		m.windowManager.AddWindow(fw)
	}
}

// openFileFromTree opens a file in the editor when triggered from
// the file tree context menu.
func (m *App) openFileFromTree(path string) (tea.Cmd, error) {
	return m.openEditorTab(path)
}

// startNewFileInTree starts a small prompt for naming a new file
// inside the given directory. Opens a focused text-input popup
// above the active editor. On Enter, the file is created (empty)
// and the tree is updated.
func (m *App) startNewFileInTree(dir string) {
	m.openTreeTextPrompt(widgets.TextPromptNewFile, "New file",
		"in "+dir, dir, "", func(value string) error {
			if value == "" {
				return fmt.Errorf("name cannot be empty")
			}
			full := filepath.Join(dir, value)
			if _, err := os.Stat(full); err == nil {
				return fmt.Errorf("already exists: %s", value)
			}
			f, err := os.Create(full)
			if err != nil {
				return err
			}
			_ = f.Close()
			m.treeModel.ProcessWatcherEvent(filetree.FileChangeEvent{
				Type: filetree.FileCreated, Path: full,
			})
			m.statusbar.SetInfo("Created " + value)
			// Open the new file in the editor so the user can
			// start typing immediately.
			if _, err := m.openEditorTab(full); err != nil {
				m.statusbar.SetError("open " + value + ": " + err.Error())
			}
			return nil
		})
}

// startNewFolderInTree starts a small prompt for naming a new
// folder inside the given directory. On Enter, the folder is
// created and the tree is updated.
func (m *App) startNewFolderInTree(dir string) {
	m.openTreeTextPrompt(widgets.TextPromptNewFolder, "New folder",
		"in "+dir, dir, "", func(value string) error {
			if value == "" {
				return fmt.Errorf("name cannot be empty")
			}
			full := filepath.Join(dir, value)
			if _, err := os.Stat(full); err == nil {
				return fmt.Errorf("already exists: %s", value)
			}
			if err := os.MkdirAll(full, 0o755); err != nil {
				return err
			}
			m.treeModel.ProcessWatcherEvent(filetree.FileChangeEvent{
				Type: filetree.FileCreated, Path: full,
			})
			// Expand the parent so the new folder is visible.
			m.treeModel.ExpandToPath(full)
			m.statusbar.SetInfo("Created folder " + value)
			return nil
		})
}

// startRenameInTree starts a rename prompt prefilled with the
// current name of the path. On Enter, the file or folder is
// renamed; if it was open in an editor, the tab key is updated
// to follow the new path.
func (m *App) startRenameInTree(path string) {
	if path == "" {
		return
	}
	base := filepath.Base(path)
	m.openTreeTextPrompt(widgets.TextPromptRename, "Rename",
		path, "", base, func(value string) error {
			if value == "" {
				return fmt.Errorf("name cannot be empty")
			}
			if value == base {
				return fmt.Errorf("name unchanged")
			}
			dir := filepath.Dir(path)
			newPath := filepath.Join(dir, value)
			if _, err := os.Stat(newPath); err == nil {
				return fmt.Errorf("target already exists: %s", value)
			}
			if err := os.Rename(path, newPath); err != nil {
				return err
			}
			m.treeModel.ProcessWatcherEvent(filetree.FileChangeEvent{
				Type: filetree.FileRenamed, OldPath: path, Path: newPath,
			})
			// If the file was open in an editor tab, retarget
			// the tab to the new path so the user keeps their
			// cursor/scroll/undo.
			if tab, ok := m.editorTabs[path]; ok {
				delete(m.editorTabs, path)
				tab.Path = newPath
				m.editorTabs[newPath] = tab
			}
			// Update the active editor pointer if the renamed
			// file is currently active.
			if m.activeEditorView == path {
				m.activeEditorView = newPath
			}
			m.statusbar.SetInfo(fmt.Sprintf("Renamed to %s", value))
			m.dirty = true
			return nil
		})
}

// startDeleteInTree starts a destructive-action confirmation. For
// directories the warning is more emphatic; for files it's brief.
// On Yes, the file or directory is removed, the tree updates, and
// any open editor tab for the file is closed.
func (m *App) startDeleteInTree(path string) {
	if path == "" {
		return
	}
	isDir := m.treeModel.IsDir(path)
	base := filepath.Base(path)

	var question, detail string
	if isDir {
		question = fmt.Sprintf("Delete folder %q and all its contents?", base)
		detail = path
	} else {
		question = fmt.Sprintf("Delete %q?", base)
		detail = path
	}
	lines := widgets.ConfirmPrompt(m.theme,
		"Delete",
		question,
		detail,
		widgets.ConfirmPromptDanger,
		false,
		60,
	)
	pop := widgets.NewPopupContent(lines)
	fw := wm.NewFloatingWindow("tree-delete-confirm", "", pop,
		60, len(lines),
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-delete-confirm")
		m.windowManager.AddWindow(fw)
	}
	m.treeDeletePending = path
	m.dirty = true
}

// openTreeTextPrompt opens the focused text-input popup used by
// new-file / new-folder / rename. Centralizes the popup creation
// so the three callers stay small. The submit callback returns a
// non-nil error to surface validation issues; the popup stays open
// in that case so the user can fix the name.
func (m *App) openTreeTextPrompt(kind widgets.TextPromptKind, title, prompt, dir, initial string, onSubmit func(value string) error) {
	if onSubmit == nil {
		return
	}
	st := &widgets.TextPromptState{
		Title:        title,
		Prompt:       prompt,
		Kind:         kind,
		InitialValue: initial,
		Value:        initial,
		Cursor:       len(initial),
		Focused:      true,
		OnSubmit:     onSubmit,
		OnCancel:     func() {},
	}
	lines := widgets.TextPromptLines(m.theme, st)
	pop := widgets.NewPopupContent(lines)
	// TextPromptLines now self-renders the unified popup chrome.
	fw := wm.NewFloatingWindow("tree-prompt", "", pop,
		widgets.TextPromptPopupWidth(st),
		widgets.TextPromptPopupHeight(st),
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-prompt")
		m.windowManager.AddWindow(fw)
	}
	m.treePrompt = st
	m.dirty = true
	_ = dir // dir is captured via closure in the caller
}

// popLongestLineWidth returns the display width of the widest
// line in the slice. Used to size confirm-style popups.
func popLongestLineWidth(lines []string) int {
	max := 0
	for _, l := range lines {
		w := rendering.VisibleWidth(l)
		if w > max {
			max = w
		}
	}
	return max
}

// closeAllEditorTabsExcept closes every editor tab except the one
// at the given path.
func (m *App) closeAllEditorTabsExcept(path string) {
	toClose := []string{}
	for p := range m.editorTabs {
		if p != path {
			toClose = append(toClose, p)
		}
	}
	for _, p := range toClose {
		m.closeEditorTabForce(p)
	}
}

// closeEditorTabsToRightOf closes every tab whose index is greater
// than the given window-tab index.
func (m *App) closeEditorTabsToRightOf(idx int) {
	tabs := m.win.Tabs()
	for i := len(tabs) - 1; i > idx; i-- {
		t := tabs[i]
		if m.isEditorTabTitle(t.Title) {
			p := m.editorPathFromTabTitle(t.Title)
			m.closeEditorTabForce(p)
		}
	}
}

// startRename arms the rename flow.
func (m *App) startRename() {
	_, _ = m.startRenameCmd(), 0
}

// refreshDirtyPrompt rebuilds the dirty-close popup's content lines
// to reflect the current focus state.
func (m *App) refreshDirtyPrompt() {
	if m.windowManager == nil {
		return
	}
	fw := m.windowManager.Window("dirty-close-prompt")
	if fw == nil {
		return
	}
	tab, ok := m.editorTabs[m.dirtyPromptPath]
	if !ok {
		return
	}
	filename := tab.Editor.Title()
	detail := fmt.Sprintf("'%s' has unsaved changes.\nClosing will discard them.", filename)
	lines := widgets.ConfirmPrompt(m.theme,
		"Unsaved changes",
		"Discard unsaved changes?",
		detail,
		widgets.ConfirmPromptWarning,
		m.dirtyPromptFocusYes,
		56,
	)
	pop := widgets.NewPopupContent(lines)
	fw.SetSize(56, len(lines))
	fw.SetContent(pop)
	fw.MarkContentDirty()
}

// handleFindReplaceKey routes keypresses when the find/replace popup
// is focused. Most keys are text input or navigation; Esc closes.
func (m *App) handleFindReplaceKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.findReplace == nil {
		return nil
	}
	fr := m.findReplace

	// Navigation/modifier keys first.
	switch msg.Code {
	case tea.KeyEsc:
		m.closeFindReplace()
		return nil
	case tea.KeyTab:
		if msg.Mod.Contains(tea.ModShift) {
			fr.FocusedField = 0
		} else {
			fr.FocusedField = 1 - fr.FocusedField
		}
		m.refreshFindReplacePopup()
		return nil
	case tea.KeyEnter:
		if fr.FocusedField == 1 && fr.ReplaceMode {
			// Replace the current match.
			if fr.OnReplace != nil {
				fr.OnReplace()
			}
			return nil
		}
		// Default: next match.
		if fr.OnNext != nil {
			fr.OnNext()
		}
		return nil
	}

	// Toggle shortcuts.
	switch msg.String() {
	case "alt+c":
		fr.CaseSensitive = !fr.CaseSensitive
		if fr.OnChange != nil {
			fr.OnChange()
		}
		return nil
	case "alt+w":
		fr.WholeWord = !fr.WholeWord
		if fr.OnChange != nil {
			fr.OnChange()
		}
		return nil
	case "alt+r":
		fr.Regex = !fr.Regex
		if fr.OnChange != nil {
			fr.OnChange()
		}
		return nil
	case "ctrl+alt+enter":
		if fr.OnReplaceAll != nil {
			fr.OnReplaceAll()
		}
		return nil
	case "shift+enter":
		if fr.OnPrev != nil {
			fr.OnPrev()
		}
		return nil
	}

	// Backspace
	if msg.Code == tea.KeyBackspace {
		if fr.FocusedField == 0 {
			if fr.Cursor > 0 && fr.Cursor <= len(fr.Query) {
				fr.Query = fr.Query[:fr.Cursor-1] + fr.Query[fr.Cursor:]
				fr.Cursor--
				if fr.OnChange != nil {
					fr.OnChange()
				}
			}
		} else if fr.FocusedField == 1 {
			if len(fr.Replace) > 0 {
				fr.Replace = fr.Replace[:len(fr.Replace)-1]
				if fr.OnChange != nil {
					fr.OnChange()
				}
			}
		}
		return nil
	}
	if msg.Code == tea.KeyDelete {
		if fr.FocusedField == 0 {
			if fr.Cursor < len(fr.Query) {
				fr.Query = fr.Query[:fr.Cursor] + fr.Query[fr.Cursor+1:]
				if fr.OnChange != nil {
					fr.OnChange()
				}
			}
		} else if fr.FocusedField == 1 {
			if len(fr.Replace) > 0 {
				fr.Replace = fr.Replace[:len(fr.Replace)-1]
				if fr.OnChange != nil {
					fr.OnChange()
				}
			}
		}
		return nil
	}

	// Arrow keys for cursor movement within the find field.
	if msg.Code == tea.KeyLeft {
		if fr.FocusedField == 0 && fr.Cursor > 0 {
			fr.Cursor--
			m.refreshFindReplacePopup()
		}
		return nil
	}
	if msg.Code == tea.KeyRight {
		if fr.FocusedField == 0 && fr.Cursor < len(fr.Query) {
			fr.Cursor++
			m.refreshFindReplacePopup()
		}
		return nil
	}

	// Regular character input. Bubble Tea gives us the rune in msg.Text
	// (preferred) or msg.Code (fallback for special keys).
	if msg.Text != "" {
		if fr.FocusedField == 0 {
			fr.Query = fr.Query[:fr.Cursor] + msg.Text + fr.Query[fr.Cursor:]
			fr.Cursor += len(msg.Text)
		} else {
			fr.Replace += msg.Text
		}
		if fr.OnChange != nil {
			fr.OnChange()
		}
		return nil
	}

	return nil
}

func (m *App) activeEditorPath() string {
	return m.activeEditorView
}

func (m *App) Init() tea.Cmd {
	if m.Mode == ModeChat {
		m.startLSPForWorkspace()
	}
	// Wire the permanent diagnostics callback so project-wide diagnostics
	// (which arrive as soon as the LSP server connects) always update
	// the file tree, even before any file is opened in the editor.
	m.lspManager.SetOnDiagnostics(func(fp string, diags []editor.LSPDiagnostic) {
		if m.treeModel != nil {
			errs, warns, hints := 0, 0, 0
			for _, d := range diags {
				switch d.Severity {
				case editor.DiagError:
					errs++
				case editor.DiagWarning:
					warns++
				default:
					hints++
				}
			}
			m.treeModel.SetDiagnostics(fp, errs, warns, hints)
		}
		if fp == m.editor.FilePath() && m.editor.IsOpen() {
			m.editor.SetDiagnostics(diags)
		}
	})

	// Hook file tree watcher events into the LSP server so it learns about
	// disk changes (git checkout, external edits, etc.).
	if m.treeModel != nil {
		m.treeModel.SetOnFileChanged(func(filePath string, changeType int) {
			if m.lspManager == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			// Send the standard workspace/didChangeWatchedFiles notification.
			m.lspManager.NotifyWatchedFilesChanged(ctx, filePath, changeType)
			// Also push the new disk content via didChange so the server
			// re-analyzes files that were bulk-opened via openProjectFiles.
			// The active editor file is excluded — it's handled by the
			// editor's own Reload + UpdateFile path to avoid clobbering
			// unsaved edits.
			m.lspManager.SyncFileFromDisk(ctx, filePath, m.editor.FilePath(), changeType)
		})
		if err := m.treeModel.StartWatcher(); err != nil {
			if m.logger != nil {
				m.logger.Warn("FILETREE", "watcher_start_failed", "error", err.Error())
			}
		}
	}

	return tea.Batch(tickCmd())
}

// startLSPForWorkspace scans the working directory and starts LSP
// servers for detected project roots.
func (m *App) startLSPForWorkspace() {
	wsDir, err := os.Getwd()
	if err != nil {
		wsDir = "."
	}
	if m.logger != nil {
		m.logger.Debug("LSP", "starting_for_workspace", "dir", wsDir)
	}
	if m.lspManager != nil {
		// Clear cached diagnostics on every (re)start so a stopped
		// server doesn't leave stale errors visible until the new
		// session republishes them.
		m.lspManager.ClearDiagnostics()
		// Also clear per-editor diagnostic caches for open tabs.
		// The new server will repopulate them via the onDiagnostics
		// callback as it walks the project.
		m.clearOpenEditorsDiagnostics()
		m.lspManager.StartForWorkspace(wsDir)
	}
}

// clearOpenEditorsDiagnostics wipes the diagnostic cache for every
// open editor tab. Called on LSP restart to avoid showing stale
// errors from the previous server session.
func (m *App) clearOpenEditorsDiagnostics() {
	if m.editor != nil {
		m.editor.SetDiagnostics(nil)
	}
	for _, tab := range m.editorTabs {
		if tab != nil && tab.Editor != nil {
			tab.Editor.SetDiagnostics(nil)
		}
	}
}

// openTextOverlayWindow opens the text overlay as a floating window.
func (m *App) openTextOverlayWindow() {
	m.windowManager.RemoveWindow("textoverlay")

	textViewFn := func() string {
		// Use the floating window's content area dimensions, not the full
		// screen dimensions. ViewInner renders content directly (no
		// RenderFloatingBox wrapper) since the FloatingWindow already
		// provides its own border and title bar.
		w, h := m.width, m.height
		if fw := m.windowManager.Window("textoverlay"); fw != nil {
			b := fw.ContentBounds()
			w, h = b.W, b.H
		}
		lines := m.textOverlay.ViewInner(w, h, m.spinFrame)
		// Join lines into a single string
		result := ""
		for i, l := range lines {
			if i > 0 {
				result += "\n"
			}
			result += l
		}
		return result
	}

	textWidget := rendering.NewANSIAdapter(textViewFn)

	win := wm.NewFloatingWindow("textoverlay", "Edit", textWidget,
		72, 30,
		wm.WithMinSize(32, 8),
	)
	wm.CenterOnScreen(win, m.width, m.height)
	m.windowManager.AddWindow(win)
}

// openFilePickerWindow opens the file picker as a floating window.
// The file picker now draws its own border/title (unified chrome),
// so the FloatingWindow is undecorated.
func (m *App) openFilePickerWindow() {
	m.windowManager.RemoveWindow("filepicker")

	win := wm.NewFloatingWindow("filepicker", "", &m.filePicker,
		60, 20,
		wm.WithDecoration(false),
		wm.WithClosable(false),
		wm.WithResizable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithMinSize(32, 10),
	)
	wm.CenterOnScreen(win, m.width, m.height)
	m.windowManager.AddWindow(win)
}

// StopLSP shuts down all LSP server connections.
func (m *App) StopLSP() {
	if m.lspManager != nil {
		m.lspManager.Stop()
	}
}

// handleQuit implements hierarchical quit: close tab first, quit if no tabs.
func (m *App) handleQuit() (tea.Model, tea.Cmd) {
	// First, close any focused floating window
	if m.windowManager != nil {
		if fw := m.windowManager.Focused(); fw != nil {
			m.windowManager.RemoveWindow(fw.ID)
			return m, nil
		}
	}

	// If there's an active overlay tab, close it first
	if m.overlay.IsOpen() {
		m.overlay.CloseAll()
		if m.Mode != ModeChat {
			m.Mode = ModeChat
			m.input.ResetPrompt()
			m.input.ClearInput()
		}
		return m, nil
	}

	// If there's an editor open, close it
	if m.activeEditorView != "" {
		m.closeEditorTab()
		return m, nil
	}

	// If there are window tabs, close the active one
	if m.win.IsOpen() && m.win.TabCount() > 0 {
		activeIdx := m.win.ActiveIndex()
		if activeIdx >= 0 {
			tab := m.win.Tabs()[activeIdx]
			action := m.win.CloseTab(activeIdx)
			if action == window.CloseNone {
				return m, nil
			}
		// If the tab was an agent tab, clean up
		if m.isAgentTabTitle(tab.Title) {
			agentID := m.agentIDFromTabTitle(tab.Title)
			m.cleanupAgent(agentID)
		}
			m.dirty = true
			// If no tabs left, show launcher
			if len(m.agents) == 0 && m.activeEditorView == "" && !m.win.IsOpen() {
				if m.onListSessions != nil {
					m.launcher = NewLauncherState(m.onListSessions())
					m.lastInteractionTime = time.Now()
				}
				return m, nil
			}
		}
	}

	// No tabs left and no sessions — quit
	m.Quit = true
	return m, tea.Quit
}

// toggleComposer opens or closes the composer floating window.
func (m *App) toggleComposer() (tea.Model, tea.Cmd) {
	if m.windowManager.Window("composer") != nil {
		m.windowManager.RemoveWindow("composer")
		return m, nil
	}

	m.composer.Open("")
	viewFn := func() string {
		w, h := m.width, m.height
		if fw := m.windowManager.Window("composer"); fw != nil {
			b := fw.ContentBounds()
			w, h = b.W, b.H
		}
		return m.composer.View(w, h)
	}
	ansiWidget := rendering.NewANSIAdapter(viewFn)

	fw := wm.NewFloatingWindow("composer", "Composer", ansiWidget,
		72, 24,
		wm.WithMinSize(40, 10),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.windowManager.AddWindow(fw)
	return m, nil
}

// openOrchestratorSettings opens the settings to the current orchestrator's
// category settings, allowing per-orchestrator model/provider overrides.
func (m *App) openOrchestratorSettings() {
	m.settings.SaveSnapshot()
	m.settings.OpenCategory("default")
	m.setMode(ModeSettings)
}

// resetOrchestratorOverrides clears all runtime overrides for the current orchestrator.
func (m *App) resetOrchestratorOverrides() {
	id := m.activeAgentView
	if id == "" {
		id = m.activeEditorView
		if id == "" {
			m.statusbar.SetInfo("No active orchestrator to reset")
			return
		}
	}
	if m.OnOrchestratorReset != nil {
		go m.OnOrchestratorReset(id)
		m.statusbar.SetInfo("Orchestrator overrides reset")
	}
}

// closeAllWindows removes all floating windows.
func (m *App) closeAllWindows() {
	for _, w := range m.windowManager.Windows() {
		m.windowManager.RemoveWindow(w.ID)
	}
}
