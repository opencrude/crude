package viewmodel

import (
	"database/sql"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/promptref"
	"crude/provider"
	"crude/sessiondb"
	"crude/tui/compositor"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/settings"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
	"crude/tui/widget"
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
	ModeLauncher
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
	mainMsgList  *widgets.MessageList
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

	blocking      bool
	yolo          bool
	tabBarFocused bool

	activeAgentView string
	agentLabels     map[string]string
	agentModels     map[string]string

	inputCh  chan<- provider.UserInput
	btwCh    chan<- provider.UserInput
	cancelCh chan<- struct{}

	OnQuit              func()
	OnExitMessage       func()
	OnSettingsSave      func(ttypes.Settings) error
	OnConfirmResult     func(ttypes.ConfirmResult)
	OnQuestionResult    func(ttypes.QuestionResult)
	OnYoloCmd           func(bool)
	OnUndoCmd           func(string)
	OnResumeSession     func(string)
	OnCancelSubAgent    func(agentID string)
	OnHardCancel        func()
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

func NewApp(modelName string, username string, inputCh chan<- provider.UserInput, btwCh chan<- provider.UserInput, cancelCh chan<- struct{}, initial ttypes.Settings, onSave func(ttypes.Settings) error, fetchFn func(providerID, baseURL, apiKey string) tea.Cmd, getProviderConfigFn func(string) ttypes.ProviderConfig, lg Logger, sessions []sessiondb.SessionMeta, sessionID, sessionDir string, db *sql.DB) *App {
	th := theme.DefaultDark()
	ml := widgets.NewMessageList(th)
	ml.SetModelName(modelName)
	ml.SetUsername(username)
	mainAgent := NewAgentState(MainAgentID, ml, lg)
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

	// Initialize compositing engine
	comp := compositor.NewCompositor(80, 24) // Will be resized on first WindowSizeMsg
	wmgr := wm.NewWindowManager()
	rctx := widget.NewRenderContext(th)

	hasSessions := len(sessions) > 0
	var initialMode Mode
	var launcher *LauncherState
	if hasSessions {
		initialMode = ModeLauncher
		launcher = NewLauncherState(sessions)
	} else {
		initialMode = ModeChat
	}

	app := &App{
		theme:               th,
		Mode:                initialMode,
		agents:              map[string]*AgentState{MainAgentID: mainAgent},
		paneMgr:             pm,
		win:                 w,
		overlay:             ov,
		compositor:          comp,
		windowManager:       wmgr,
		renderCtx:           rctx,
		mainMsgList:         ml,
		input:               im,
		statusbar:           sb,
		settings:            sm,
		confirm:             cd,
		questionForm:        qd,
		yoloDlg:             yd,
		textOverlay:         to,
		filePicker:          fp,
		refStore:            refs,
		agentLabels:         make(map[string]string),
		agentModels:         make(map[string]string),
		inputCh:             inputCh,
		btwCh:               btwCh,
		cancelCh:            cancelCh,
		modelName:           modelName,
		username:            username,
		startTime:           time.Now(),
		lastCancelTime:      time.Now(),
		lastInteractionTime: time.Now(),
		lastSettings:        initial,
		OnSettingsSave:      onSave,
		logger:              lg,
		launcher:            launcher,
	}

	ml.SetSubPaneContentFn(app.renderSubPaneContent)
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
	if agentID == MainAgentID || agentID == "" {
		return
	}

	agent, agentOK := m.agents[agentID]
	if agentOK && agent.Completed {
		return // idempotent: already completed
	}

	title := m.agentTabTitle(agentID)
	if tab := m.win.TabByTitle(title); tab != nil {
		wasActive := m.win.IndexOf(tab.ID) == m.win.ActiveIndex()
		m.win.CloseTabByID(tab.ID)
		if wasActive && m.activeAgentView == agentID {
			m.activeAgentView = ""
			if m.mainMsgList != nil {
				m.mainMsgList.InvalidateAllMessages()
			}
		}
	}

	if sp := m.paneMgr.Get(agentID); sp != nil && !sp.Done {
		sp.Collapse()
		m.mainMsgList.InvalidateSubPane(agentID)
	}

	if agentOK {
		agent.Completed = true
		agent.Streaming = false
		agent.Thinking = false
		if agent.Status == "" || agent.Status == "running" {
			agent.Status = "completed"
		}
	}

	m.statusbar.ClearAgentState(agentID)

	if !m.win.IsOpen() {
		m.setMode(ModeChat)
	} else {
		m.syncOverlayMode()
	}
}

func (m *App) mainAgent() *AgentState {
	return m.agents[MainAgentID]
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

func (m *App) mainAgentActive() bool {
	if ma, ok := m.agents[MainAgentID]; ok {
		return !ma.Completed && ma.IsActive()
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
	m.logger.Debug("UI", "mode_switch", "from", modeName(oldMode), "to", modeName(newMode))
	m.Mode = newMode
	m.mainMsgList.ClearSelection()
	m.mainMsgList.InvalidateAllMessages()
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
		m.windowManager.RemoveWindow("textoverlay")
		m.windowManager.RemoveWindow("filepicker")
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
	case ModeLauncher:
		return "launcher"
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

func (m *App) SetReferences(refs []promptref.Reference) {
	if m.refStore == nil {
		return
	}
	for _, ref := range refs {
		m.refStore.AddExisting(ref)
	}
	m.input.SetReferenceStore(m.refStore)
}

func (m *App) activeMessageList() *widgets.MessageList {
	if m.activeAgentView != "" && m.activeAgentView != MainAgentID {
		if agent, ok := m.agents[m.activeAgentView]; ok && agent.MsgList != nil {
			return agent.MsgList
		}
	}
	return m.mainMsgList
}

func (m *App) agentForView() *AgentState {
	if m.activeAgentView == "" || m.activeAgentView == MainAgentID {
		return m.mainAgent()
	}
	return m.agents[m.activeAgentView]
}

func (m *App) activeAgentID() string {
	if m.activeAgentView == "" {
		return MainAgentID
	}
	return m.activeAgentView
}

func (m *App) switchToAgentView(agentID string) {
	prev := m.activeAgentView
	if agentID == MainAgentID || agentID == "" {
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
		if m.mainMsgList != nil {
			m.mainMsgList.InvalidateAllMessages()
		}
	}

	m.syncAgentWindowTabs()
}

// agentTabTitle returns the window tab title for an agent.
// Uses the full agentID to guarantee uniqueness (no truncation).
// Human-readable display names are set via DisplayTitle on the tab.
func (m *App) agentTabTitle(agentID string) string {
	if agentID == MainAgentID || agentID == "" {
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
	id := title[6:]
	if id == "main" {
		return MainAgentID
	}
	return id
}

// syncAgentWindowTabs creates/removes window tabs to match the current agent set.
// Each agent gets its own window tab (like question tabs), so the window system's
// vertical sidebar handles the ❯ markers, clicks, and ctrl+]/ctrl+[ natively.
func (m *App) syncAgentWindowTabs() {
	activeSubAgents := 0
	for id, a := range m.agents {
		if id != MainAgentID && !a.Completed {
			activeSubAgents++
		}
	}

	if activeSubAgents == 0 {
		m.purgeAgentTabs()
		return
	}

	activeID := m.activeAgentView
	if activeID == "" {
		activeID = MainAgentID
	}

	// Main agent tab always first. It must be present whenever sub-agents are
	// active so the user can switch back to the orchestrator. The main agent's
	// Completed flag toggles between turns (true when idle, false when active);
	// basing the tab on that flag causes the tab to flicker in and out of
	// existence every turn boundary.
	title := m.agentTabTitle(MainAgentID)
	if m.win.TabByTitle(title) == nil {
		m.win.OpenTab(title, "", false)
	}
	if tab := m.win.TabByTitle(title); tab != nil {
		newIcon := m.agentTabIcon(MainAgentID)
		if tab.Icon != newIcon {
			m.win.SetTabIconByIndex(m.win.IndexOf(tab.ID), newIcon)
		}
		if tab.DisplayTitle != "main" {
			m.win.SetTabDisplayTitleByIndex(m.win.IndexOf(tab.ID), "main")
		}
	}

	// Sub-agent tabs in pane creation order (deterministic).
	for _, id := range m.paneMgr.paneOrder {
		if id == MainAgentID {
			continue
		}
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

	activeTitle := m.agentTabTitle(activeID)
	if tab := m.win.TabByTitle(activeTitle); tab != nil {
		targetIdx := m.win.IndexOf(tab.ID)
		if m.win.ActiveIndex() != targetIdx {
			m.win.SwitchTo(targetIdx)
		}
	}

	m.purgeStaleAgentTabs()
}

// agentTabIcon returns the status icon for an agent's window tab.
func (m *App) agentTabIcon(id string) string {
	if id == MainAgentID || id == "" {
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
// or have completed. The main agent tab is never purged here as long as
// at least one sub-agent is still active — the main agent is the
// orchestrator and its tab must remain accessible between turns even when
// the main agent is idle (Completed=true).
func (m *App) purgeStaleAgentTabs() {
	hasActiveSubs := false
	for id, a := range m.agents {
		if id != MainAgentID && !a.Completed {
			hasActiveSubs = true
			break
		}
	}

	for i := m.win.TabCount() - 1; i >= 0; i-- {
		tabs := m.win.Tabs()
		if i >= len(tabs) {
			continue
		}
		if m.isAgentTabTitle(tabs[i].Title) {
			agentID := m.agentIDFromTabTitle(tabs[i].Title)
			// Preserve the main agent tab while sub-agents are active.
			if agentID == MainAgentID && hasActiveSubs {
				continue
			}
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

func (m *App) Init() tea.Cmd {
	return tea.Batch(tickCmd())
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
		wm.WithDecoration(true),
		wm.WithMovable(true),
		wm.WithResizable(true),
		wm.WithClosable(true),
		wm.WithBorderStyle(wm.BorderRounded),
		wm.WithMinSize(32, 8),
	)
	wm.CenterOnScreen(win, m.width, m.height)
	m.windowManager.AddWindow(win)
}

// openFilePickerWindow opens the file picker as a floating window.
func (m *App) openFilePickerWindow() {
	m.windowManager.RemoveWindow("filepicker")

	fpViewFn := func() string {
		// Use the floating window's content area dimensions, not the full
		// screen dimensions. ViewInner renders content directly (no
		// RenderFloatingBox wrapper) since the FloatingWindow already
		// provides its own border and title bar.
		w, h := m.width, m.height
		if fw := m.windowManager.Window("filepicker"); fw != nil {
			b := fw.ContentBounds()
			w, h = b.W, b.H
		}
		lines := m.filePicker.ViewInner(w, h, m.spinFrame)
		result := ""
		for i, l := range lines {
			if i > 0 {
				result += "\n"
			}
			result += l
		}
		return result
	}

	fpWidget := rendering.NewANSIAdapter(fpViewFn)

	win := wm.NewFloatingWindow("filepicker", "File Picker", fpWidget,
		60, 20,
		wm.WithDecoration(true),
		wm.WithMovable(true),
		wm.WithResizable(true),
		wm.WithClosable(true),
		wm.WithBorderStyle(wm.BorderRounded),
		wm.WithMinSize(32, 10),
	)
	wm.CenterOnScreen(win, m.width, m.height)
	m.windowManager.AddWindow(win)
}

// closeAllWindows removes all floating windows.
func (m *App) closeAllWindows() {
	for _, w := range m.windowManager.Windows() {
		m.windowManager.RemoveWindow(w.ID)
	}
}
