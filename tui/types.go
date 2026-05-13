package tui

import (
	"time"

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
	chatAreaHeight int

	spinFrame int
	history   []string

	tokenPrompt, tokenCompletion, tokenCached         int
	sessionCost                                       float64
	contextWindow, contextUsed                        int

	lastSettings Settings

	formHint string
}

// Public API types — type aliases so consumers use tui.Settings etc.
type (
	Settings       = ttypes.Settings
	ModelEntry     = ttypes.ModelEntry
	ProviderConfig = ttypes.ProviderConfig
	ProviderPreset = ttypes.ProviderPreset
	ConfirmResult  = ttypes.ConfirmResult
	UndoResultMsg  = ttypes.UndoResultMsg
)

type ChatMessage = ttypes.ChatMessage

type AgentEventMsg = ttypes.AgentEventMsg

const (
	ToolPreviewPreparing = ttypes.ToolPreviewPreparing
	ToolPreviewStreaming  = ttypes.ToolPreviewStreaming
	ToolPreviewDone      = ttypes.ToolPreviewDone
	ToolPreviewError     = ttypes.ToolPreviewError
)

const (
	RoleUser      = ttypes.RoleUser
	RoleAssistant = ttypes.RoleAssistant
	RoleThinking  = ttypes.RoleThinking
	RoleTool      = ttypes.RoleTool
	RoleError     = ttypes.RoleError
)

const (
	ConfirmAllow  = ttypes.ConfirmAllow
	ConfirmAlways = ttypes.ConfirmAlways
	ConfirmDeny   = ttypes.ConfirmDeny
)

type (
	ModelsListMsg        = ttypes.ModelsListMsg
	ModelsErrorMsg       = ttypes.ModelsErrorMsg
	OracleModelsListMsg  = ttypes.OracleModelsListMsg
	OracleModelsErrorMsg = ttypes.OracleModelsErrorMsg
)

type tickMsg = ttypes.TickMsg
type errorMsg = ttypes.ErrorMsg
type statusMsg = ttypes.StatusMsg
type agentMsg = ttypes.AgentMsg
type openSettingsMsg = ttypes.OpenSettingsMsg
type fetchModelsMsg = ttypes.FetchModelsMsg
type saveSettingsMsg = ttypes.SaveSettingsMsg
type updateModelMsg = ttypes.UpdateModelMsg
type selectionTickMsg = ttypes.SelectionTickMsg
type clipboardCopiedMsg = ttypes.ClipboardCopiedMsg
type clipboardErrorMsg = ttypes.ClipboardErrorMsg
