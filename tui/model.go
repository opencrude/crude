package tui

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

type mode int

const (
	modeChat mode = iota
	modeSettings
	modeConfirm
	modeYolo
)

type App struct {
	theme  *theme.Theme
	mode   mode
	width  int
	height int

	chat      widgets.ChatModel
	input     input.Model
	statusbar *widgets.StatusBar
	settings  settings.Model
	confirm   widgets.ConfirmDialog
	yoloDlg   widgets.YoloDialog

	thinking  bool
	blocking  bool
	streaming bool
	yolo      bool

	inputCh  chan<- string
	cancelCh chan<- struct{}

	OnQuit, OnExitMessage               func()
	OnSettingsSave                      func(Settings)
	OnConfirmResult                     func(ConfirmResult)
	OnYoloCmd                           func(bool)
	OnUndoCmd                           func(string)
	FetchModelsFn                       func(string, string, string) ([]ModelEntry, error)
	GetProviderConfigFn                 func(string) ProviderConfig
	ProviderPresets                     []ProviderPreset

	quit           bool
	lastCancelTime time.Time
	modelName      string
	username       string
	startTime      time.Time

	spinFrame int
	history   []string

	tokenPrompt, tokenCompletion, tokenCached         int
	sessionCost                                       float64
	contextWindow, contextUsed                        int

	lastSettings Settings
}

func NewApp(modelName string, username string, inputCh chan<- string, cancelCh chan<- struct{}, initial Settings, onSave func(Settings), fetchFn func(providerID, baseURL, apiKey string) tea.Cmd, getProviderConfigFn func(providerID string) ProviderConfig) *App {
	th := theme.DefaultDark()
	cm := widgets.NewChatModel(th)
	im := input.NewModel(th)
	sb := widgets.NewStatusBar(th)
	sm := settings.NewModel(th, initial, nil, getProviderConfigFn, fetchFn)
	cd := widgets.NewConfirmDialog(th)
	yd := widgets.NewYoloDialog(th)

	return &App{
		theme:               th,
		chat:                cm,
		input:               im,
		statusbar:           sb,
		settings:            sm,
		confirm:             cd,
		yoloDlg:             yd,
		inputCh:             inputCh,
		cancelCh:            cancelCh,
		modelName:           modelName,
		username:            username,
		startTime:           time.Now(),
		lastCancelTime:      time.Now(),
		lastSettings:        initial,
	}
}

func (m *App) SetProviderPresets(presets []ProviderPreset) {
	m.ProviderPresets = presets
	m.settings.SetPresets(presets)
}

func (m *App) SetHistory(history []string) {
	m.history = history
	m.input.SetHistory(history)
}

func (m *App) History() []string {
	return m.history
}

func (m *App) Init() tea.Cmd {
	return tea.Batch(tickCmd(), tea.EnterAltScreen)
}

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return ttypes.TickMsg(t)
	})
}
