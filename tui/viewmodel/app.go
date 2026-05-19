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
	confirm    widgets.ConfirmDialog
	yoloDlg    widgets.YoloDialog

	win *window.Window

	blocking  bool
	yolo      bool

	inputCh  chan<- string
	btwCh    chan<- string
	cancelCh chan<- struct{}

	OnQuit             func()
	OnExitMessage      func()
	OnSettingsSave     func(ttypes.Settings) error
	OnConfirmResult    func(ttypes.ConfirmResult)
	OnYoloCmd          func(bool)
	OnUndoCmd          func(string)
	OnResumeSession    func(string)
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
	yd := widgets.NewYoloDialog(th)

	rendering.SetInvalidUTF8Logger(func(tag, msg string, pairs ...any) { lg.Warn(tag, msg, pairs...) })

	pm := NewPaneManager()

	w := window.New(th)

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
		mainMsgList:  ml,
		input:        im,
		statusbar:    sb,
		settings:     sm,
		confirm:      cd,
		yoloDlg:      yd,
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

func (m *App) mainAgent() *AgentState {
	return m.agents[MainAgentID]
}

func (m *App) anyActive() bool {
	for _, a := range m.agents {
		if a.IsActive() {
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
		m.win.OpenTabWithLayout("Settings", "", true, window.TabVertical)
	case ModeConfirm:
		m.win.OpenTabWithLayout("Confirm", "", true, window.TabVertical)
	case ModeYolo:
		m.win.OpenTabWithLayout("YOLO", "", true, window.TabVertical)
	case ModeChat:
		m.formHint = ""
		if m.win.HasClosableTabs() {
			m.win.CloseAll()
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

func (m *App) Init() tea.Cmd {
	return tea.Batch(tickCmd(), tea.EnterAltScreen)
}
