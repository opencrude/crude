package types

import model "crude/types"

const (
	ToolPreviewPreparing = model.ToolPreviewPreparing
	ToolPreviewStreaming = model.ToolPreviewStreaming
	ToolPreviewDone      = model.ToolPreviewDone
	ToolPreviewError     = model.ToolPreviewError
)

const (
	ToolStatusCalling   = model.ToolStatusCalling
	ToolStatusStreaming = model.ToolStatusStreaming
	ToolStatusSuccess   = model.ToolStatusSuccess
	ToolStatusError     = model.ToolStatusError
	ToolStatusCancelled = model.ToolStatusCancelled
)

const (
	RoleUser          = model.RoleUser
	RoleAssistant     = model.RoleAssistant
	RoleThinking      = model.RoleThinking
	RoleTool          = model.RoleTool
	RoleError         = model.RoleError
	RoleStatus        = model.RoleStatus
	RoleAssignment    = model.RoleAssignment
	RoleSessionPicker = model.RoleSessionPicker
)

const (
	ConfirmAllow  = model.ConfirmAllow
	ConfirmAlways = model.ConfirmAlways
	ConfirmDeny   = model.ConfirmDeny
)

type (
	CategorySettings     = model.CategorySettings
	SafetySettings       = model.SafetySettings
	LocalHistorySettings = model.LocalHistorySettings
	CommandSeverityRule  = model.CommandSeverityRule
	ProfileSettings      = model.ProfileSettings
	AgentsSettings       = model.AgentsSettings
	MemorySettings       = model.MemorySettings
	Settings             = model.Settings
	AppSettings          = model.AppSettings
	SessionSettings      = model.SessionSettings
	WorkspaceOverrides   = model.WorkspaceOverrides
	SettingsScope        = model.SettingsScope
	ThemeSettings        = model.ThemeSettings
	EditorSettings       = model.EditorSettings
	LSPSettings          = model.LSPSettings
	LSPLanguageSettings  = model.LSPLanguageSettings
	LSPLanguage          = model.LSPLanguage
	UISettings           = model.UISettings
	FontSettings         = model.FontSettings
	ModelEntry           = model.ModelEntry
	ProviderPreset       = model.ProviderPreset
	ConfirmResult        = model.ConfirmResult
	QuestionResult       = model.QuestionResult
	UndoResultMsg        = model.UndoResultMsg
	CompactResultMsg     = model.CompactResultMsg
	ChatMessage          = model.ChatMessage
	AgentEventMsg        = model.AgentEventMsg
	TickMsg              = model.TickMsg
	StatusMsg            = model.StatusMsg
	ErrorMsg             = model.ErrorMsg
	AgentMsg             = model.AgentMsg
	OpenSettingsMsg      = model.OpenSettingsMsg
	ConfigReloadedMsg    = model.ConfigReloadedMsg
	FetchModelsMsg       = model.FetchModelsMsg
	SaveSettingsMsg      = model.SaveSettingsMsg
	UpdateModelMsg       = model.UpdateModelMsg
	ModelsListMsg        = model.ModelsListMsg
	ModelsErrorMsg       = model.ModelsErrorMsg
	SelectionTickMsg     = model.SelectionTickMsg
	HistoryMessagesMsg   = model.HistoryMessagesMsg
)

const (
	SettingsScopeApplication = model.SettingsScopeApplication
	SettingsScopeWorkspace   = model.SettingsScopeWorkspace
)

const (
	DefaultMemoryRecallBudget       = model.DefaultMemoryRecallBudget
	DefaultMemoryRecallLimit        = model.DefaultMemoryRecallLimit
	DefaultMemoryExtractCadence     = model.DefaultMemoryExtractCadence
	DefaultContextCompactionPercent = model.DefaultContextCompactionPercent
)

var (
	ApplyAppDefaults           = model.ApplyAppDefaults
	NormalizeLSP               = model.NormalizeLSP
	DefaultLSPSettings         = model.DefaultLSPSettings
	DefaultLSPLanguages        = model.DefaultLSPLanguages
	MergeAppWorkspace          = model.MergeAppWorkspace
	OverridesFromDiff          = model.OverridesFromDiff
	WorkspaceOverridesFromDiff = model.WorkspaceOverridesFromDiff
)
