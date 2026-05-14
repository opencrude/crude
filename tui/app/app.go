package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/input"
	"crude/tui/settings"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
)

var OnHistorySave func([]string)

type Mode int

const (
	ModeChat Mode = iota
	ModeSettings
	ModeConfirm
	ModeYolo
)

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

	blocking  bool
	yolo      bool

	inputCh  chan<- string
	cancelCh chan<- struct{}

	OnQuit             func()
	OnExitMessage      func()
	OnSettingsSave     func(ttypes.Settings) error
	OnConfirmResult    func(ttypes.ConfirmResult)
	OnYoloCmd          func(bool)
	OnUndoCmd          func(string)
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

	lastSettings ttypes.Settings

	formHint string
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

func NewApp(modelName string, username string, inputCh chan<- string, cancelCh chan<- struct{}, initial ttypes.Settings, onSave func(ttypes.Settings) error, fetchFn func(providerID, baseURL, apiKey string, isOracle bool) tea.Cmd, getProviderConfigFn func(string) ttypes.ProviderConfig) *App {
	th := theme.DefaultDark()
	ml := widgets.NewMessageList(th)
	ml.SetModelName(modelName)
	ml.SetUsername(username)
	mainAgent := NewAgentState(MainAgentID, ml)
	im := input.NewModel(th)
	sb := widgets.NewStatusBar(th)
	sm := settings.NewModel(th, initial, nil, getProviderConfigFn, fetchFn)
	cd := widgets.NewConfirmDialog(th)
	yd := widgets.NewYoloDialog(th)

	pm := NewPaneManager()

	app := &App{
		theme:        th,
		agents:       map[string]*AgentState{MainAgentID: mainAgent},
		paneMgr:      pm,
		mainMsgList:  ml,
		input:        im,
		statusbar:    sb,
		settings:     sm,
		confirm:      cd,
		yoloDlg:      yd,
		inputCh:      inputCh,
		cancelCh:     cancelCh,
		modelName:    modelName,
		username:     username,
		startTime:    time.Now(),
		lastCancelTime: time.Now(),
		lastSettings: initial,
		OnSettingsSave: onSave,
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
	m.Mode = newMode
	m.mainMsgList.InvalidateAllMessages()
	switch newMode {
	case ModeSettings:
		m.input.ClearInput()
	case ModeChat:
		m.formHint = ""
	}
}

func (m *App) History() []string {
	return m.history
}

func (m *App) Init() tea.Cmd {
	return tea.Batch(tickCmd(), tea.EnterAltScreen)
}
