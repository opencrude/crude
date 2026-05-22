package viewmodel

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"crude/sessiondb"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/settings"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
	"crude/tui/window"
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

	agents     map[string]*AgentState
	paneMgr    *PaneManager
	mainMsgList *widgets.MessageList
	input      input.Model
	statusbar  *widgets.StatusBar
	settings   settings.Model
	confirm      widgets.ConfirmDialog
	questionForm  widgets.QuestionForm
	yoloDlg      widgets.YoloDialog

	win     *window.Window
	overlay *window.Window

	blocking       bool
	yolo           bool
	tabBarFocused  bool

	activeAgentView string
	agentLabels     map[string]string
	agentStatuses   map[string]string

	inputCh  chan<- string
	btwCh    chan<- string
	cancelCh chan<- struct{}

	OnQuit             func()
	OnExitMessage      func()
	OnSettingsSave     func(ttypes.Settings) error
	OnConfirmResult    func(ttypes.ConfirmResult)
	OnQuestionResult   func(ttypes.QuestionResult)
	OnYoloCmd          func(bool)
	OnUndoCmd          func(string)
	OnResumeSession    func(string)
	OnCancelSubAgent   func(agentID string)
	FetchModelsFn      func(string, string, string) ([]ttypes.ModelEntry, error)
	GetProviderConfigFn func(string) ttypes.ProviderConfig
	ProviderPresets    []ttypes.ProviderPreset

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
	contextWindow, contextUsed                 int
	tokenEstimated                             bool

	lastSettings ttypes.Settings

	formHint string

	logger Logger

	// Launcher state (replaces old session picker)
	launcher             *LauncherState
	onDeleteSession      func(string)
	onListSessions       func() []sessiondb.SessionMeta
	lastInteractionTime  time.Time
}

type (
	tickMsg          = ttypes.TickMsg
	errorMsg         = ttypes.ErrorMsg
	statusMsg        = ttypes.StatusMsg
	agentMsg         = ttypes.AgentMsg
	openSettingsMsg  = ttypes.OpenSettingsMsg
	fetchModelsMsg   = ttypes.FetchModelsMsg
	saveSettingsMsg       = ttypes.SaveSettingsMsg
	updateModelMsg        = ttypes.UpdateModelMsg
	selectionTickMsg      = ttypes.SelectionTickMsg
	settingsSaveResultMsg = ttypes.SettingsSaveResultMsg
)

func NewApp(modelName string, username string, inputCh chan<- string, btwCh chan<- string, cancelCh chan<- struct{}, initial ttypes.Settings, onSave func(ttypes.Settings) error, fetchFn func(providerID, baseURL, apiKey string) tea.Cmd, getProviderConfigFn func(string) ttypes.ProviderConfig, lg Logger, sessions []sessiondb.SessionMeta) *App {
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

	rendering.SetInvalidUTF8Logger(func(tag, msg string, pairs ...any) { lg.Warn(tag, msg, pairs...) })

	pm := NewPaneManager()

	w := window.New(th)
	ov := window.New(th)

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
		theme:        th,
		Mode:         initialMode,
		agents:       map[string]*AgentState{MainAgentID: mainAgent},
		paneMgr:      pm,
		win:          w,
		overlay:      ov,
		mainMsgList:  ml,
		input:        im,
		statusbar:    sb,
		settings:     sm,
		confirm:      cd,
		questionForm:  qd,
		yoloDlg:      yd,
		agentLabels:  make(map[string]string),
		agentStatuses: make(map[string]string),
		inputCh:      inputCh,
		btwCh:        btwCh,
		cancelCh:    cancelCh,
		modelName:    modelName,
		username:     username,
		startTime:    time.Now(),
		lastCancelTime: time.Now(),
		lastInteractionTime: time.Now(),
		lastSettings: initial,
		OnSettingsSave: onSave,
		logger:       lg,
		launcher:     launcher,
	}

	ml.SetSubPaneContentFn(app.renderSubPaneContent)

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
	alreadyCompleted := agentOK && agent.Completed

	// Close the window tab FIRST (agentTabTitle depends on agentLabels,
	// which we delete later). Idempotent — TabByTitle returns nil if
	// the tab was already closed.
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

	// Mark SubPane as Done so anyActive() returns false and
	// the tick loop goes back to slow mode.
	if sp := m.paneMgr.Get(agentID); sp != nil && !sp.Done {
		sp.Collapse()
		m.mainMsgList.InvalidateSubPane(agentID)
	}

	// Mark agent as Completed so late-arriving events are ignored.
	if agentOK {
		agent.Completed = true
		agent.Streaming = false
		agent.Thinking = false
	}

	// Clean up bookkeeping maps.
	delete(m.agentLabels, agentID)
	delete(m.agentStatuses, agentID)
	m.statusbar.ClearAgentState(agentID)

	// Sync mode — if no closable tabs remain, go to chat.
	if !alreadyCompleted {
		if !m.win.IsOpen() {
			m.setMode(ModeChat)
		} else {
			m.syncOverlayMode()
		}
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
	case ModeYolo:
		m.overlay.OpenTab("YOLO", "", true)
	case ModeChat:
		m.formHint = ""
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
// Uses "agent:<label>" format so it's identifiable as an agent tab.
func (m *App) agentTabTitle(agentID string) string {
	if agentID == MainAgentID || agentID == "" {
		return "agent:main"
	}
	label := agentID
	if lbl, ok := m.agentLabels[agentID]; ok && lbl != "" {
		label = lbl
	} else if len(agentID) > 12 {
		label = agentID[:12]
	}
	return "agent:" + label
}

// isAgentTabTitle checks if a window tab title is an agent tab.
func (m *App) isAgentTabTitle(title string) bool {
	return len(title) > 6 && title[:6] == "agent:"
}

// agentIDFromTabTitle extracts the agent ID from a window tab title.
func (m *App) agentIDFromTabTitle(title string) string {
	if !m.isAgentTabTitle(title) {
		return ""
	}
	label := title[6:]
	if label == "main" {
		return MainAgentID
	}
	for id, lbl := range m.agentLabels {
		if lbl == label {
			return id
		}
	}
	if _, ok := m.agents[label]; ok {
		return label
	}
	for id := range m.agents {
		if id != MainAgentID && len(id) > 12 && id[:12] == label {
			return id
		}
	}
	return label
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

	// Main agent tab always first.
	if ma, ok := m.agents[MainAgentID]; ok && !ma.Completed {
		title := m.agentTabTitle(MainAgentID)
		if m.win.TabByTitle(title) == nil {
			m.win.OpenTab(title, "", false)
		}
		if tab := m.win.TabByTitle(title); tab != nil {
			m.win.SetTabIconByIndex(m.win.IndexOf(tab.ID), m.agentTabIcon(MainAgentID))
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
			m.win.SetTabIconByIndex(m.win.IndexOf(tab.ID), m.agentTabIcon(id))
		}
	}

	activeTitle := m.agentTabTitle(activeID)
	if tab := m.win.TabByTitle(activeTitle); tab != nil {
		m.win.SwitchTo(m.win.IndexOf(tab.ID))
	}

	m.purgeStaleAgentTabs()
}

// agentTabIcon returns the status icon for an agent's window tab.
func (m *App) agentTabIcon(id string) string {
	if id == MainAgentID || id == "" {
		return "■"
	}
	status := "running"
	if st, ok := m.agentStatuses[id]; ok && st != "" {
		status = st
	}
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

// purgeAgentTabs closes all agent window tabs.
func (m *App) purgeAgentTabs() {
	for {
		removed := false
		for i := m.win.TabCount() - 1; i >= 0; i-- {
			tabs := m.win.Tabs()
			if i >= len(tabs) {
				continue
			}
			if m.isAgentTabTitle(tabs[i].Title) {
				if m.win.CloseTab(i) != window.CloseNone {
					removed = true
				}
				break
			}
		}
		if !removed {
			break
		}
	}
}

// purgeStaleAgentTabs closes agent tabs for agents that no longer exist.
func (m *App) purgeStaleAgentTabs() {
	for {
		removed := false
		for i := m.win.TabCount() - 1; i >= 0; i-- {
			tabs := m.win.Tabs()
			if i >= len(tabs) {
				continue
			}
			if m.isAgentTabTitle(tabs[i].Title) {
				agentID := m.agentIDFromTabTitle(tabs[i].Title)
				a, ok := m.agents[agentID]
				if !ok || a.Completed {
					if m.win.CloseTab(i) != window.CloseNone {
						removed = true
					}
					break
				}
			}
		}
		if !removed {
			break
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
	case activeTab.Title == "Question" || m.isQuestionTabTitle(activeTab.Title):
		m.Mode = ModeQuestion
		if m.isQuestionTabTitle(activeTab.Title) {
			idx := m.questionTabIndex(activeTab.Title)
			if idx >= 0 {
				m.questionForm.SetFocused(idx)
				m.syncQuestionInputPrompt()
			}
		}
	default:
		m.Mode = ModeChat
		m.formHint = ""
	}
}

func (m *App) Init() tea.Cmd {
	return tea.Batch(tickCmd(), tea.EnterAltScreen)
}
