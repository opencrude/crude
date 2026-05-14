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

	msgList    *widgets.MessageList
	input      input.Model
	statusbar  *widgets.StatusBar
	settings   settings.Model
	confirm    widgets.ConfirmDialog
	yoloDlg    widgets.YoloDialog

	thinking  bool
	blocking  bool
	streaming bool
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

	tokenBuf      string
	lastTokenTime time.Time

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
	im := input.NewModel(th)
	sb := widgets.NewStatusBar(th)
	sm := settings.NewModel(th, initial, nil, getProviderConfigFn, fetchFn)
	cd := widgets.NewConfirmDialog(th)
	yd := widgets.NewYoloDialog(th)

	return &App{
		theme:          th,
		msgList:        ml,
		input:          im,
		statusbar:      sb,
		settings:       sm,
		confirm:        cd,
		yoloDlg:        yd,
		inputCh:        inputCh,
		cancelCh:       cancelCh,
		modelName:      modelName,
		username:       username,
		startTime:      time.Now(),
		lastCancelTime: time.Now(),
		lastSettings:   initial,
		OnSettingsSave: onSave,
	}
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
	m.msgList.InvalidateAllMessages()
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
