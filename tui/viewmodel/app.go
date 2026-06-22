package viewmodel

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

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
	editor                *editor.Editor
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

	if m.editor.FilePath() == path && m.editor.IsOpen() {
		if tab := m.win.TabByTitle(tabTitle); tab != nil {
			m.win.SwitchTo(m.win.IndexOf(tab.ID))
			m.handleTabSwitch()
		}
		return nil, nil
	}

	err := m.editor.Open(path)
	if err != nil {
		return nil, err
	}

	// Wire content changes to LSP manager.
	m.editor.SetOnContentChanged(func(fp, text string) {
		lspCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.lspManager.UpdateFile(lspCtx, fp, text)
	})
	// Open the file on the LSP server.
	lspCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.lspManager.OpenFile(lspCtx, path, m.editor.Value())

	// Apply cached diagnostics immediately so the editor is never empty
	// while waiting for the server to respond.
	if cached := m.lspManager.FileDiagnostics(path); len(cached) > 0 {
		m.editor.SetDiagnostics(cached)
	}

	displayTitle := m.editor.Title()

	if m.win.TabByTitle(tabTitle) == nil {
		m.win.OpenTab(tabTitle, "✎", true)
	}
	if tab := m.win.TabByTitle(tabTitle); tab != nil {
		m.win.SetTabDisplayTitleByIndex(m.win.IndexOf(tab.ID), displayTitle)
		m.win.SwitchTo(m.win.IndexOf(tab.ID))
		m.handleTabSwitch()
	}

	return m.editor.BlinkCmd(), nil
}

func (m *App) closeEditorTab() {
	if m.activeEditorPath() == "" {
		return
	}
	path := m.activeEditorPath()
	tabTitle := m.editorTabTitle(path)
	if tab := m.win.TabByTitle(tabTitle); tab != nil {
		m.win.CloseTabByID(tab.ID)
	}
	hasEditor := false
	for _, t := range m.win.Tabs() {
		if m.isEditorTabTitle(t.Title) {
			hasEditor = true
			break
		}
	}
	if !hasEditor {
		lspCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.lspManager.CloseFile(lspCtx, path)
		m.editor.Close()
		m.activeEditorView = ""
	}
	m.handleTabSwitch()
	if len(m.agents) == 0 && m.activeEditorView == "" {
		if m.onListSessions != nil {
			m.launcher = NewLauncherState(m.onListSessions())
			m.lastInteractionTime = time.Now()
		}
	}
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
			if m.lspManager != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				m.lspManager.NotifyWatchedFilesChanged(ctx, filePath, changeType)
			}
		})
		_ = m.treeModel.StartWatcher()
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
		m.lspManager.StartForWorkspace(wsDir)
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
func (m *App) openFilePickerWindow() {
	m.windowManager.RemoveWindow("filepicker")

	win := wm.NewFloatingWindow("filepicker", "File Picker", &m.filePicker,
		60, 20,
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
