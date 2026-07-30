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
	RoleSessionPicker = model.RoleSessionPicker
)

const (
	ConfirmAllow  = model.ConfirmAllow
	ConfirmAlways = model.ConfirmAlways
	ConfirmDeny   = model.ConfirmDeny
)

type (
	CategorySettings      = model.CategorySettings
	ProfileSettings       = model.ProfileSettings
	AgentsSettings        = model.AgentsSettings
	Settings              = model.Settings
	AppSettings           = model.AppSettings
	SessionSettings       = model.SessionSettings
	ThemeSettings         = model.ThemeSettings
	EditorSettings        = model.EditorSettings
	LSPSettings           = model.LSPSettings
	UISettings            = model.UISettings
	FontSettings          = model.FontSettings
	ModelEntry            = model.ModelEntry
	ProviderConfig        = model.ProviderConfig
	ProviderPreset        = model.ProviderPreset
	ConfirmResult         = model.ConfirmResult
	QuestionResult        = model.QuestionResult
	UndoResultMsg         = model.UndoResultMsg
	ChatMessage           = model.ChatMessage
	AgentEventMsg         = model.AgentEventMsg
	TickMsg               = model.TickMsg
	StatusMsg             = model.StatusMsg
	ErrorMsg              = model.ErrorMsg
	AgentMsg              = model.AgentMsg
	OpenSettingsMsg       = model.OpenSettingsMsg
	ConfigReloadedMsg     = model.ConfigReloadedMsg
	FetchModelsMsg        = model.FetchModelsMsg
	SaveSettingsMsg       = model.SaveSettingsMsg
	UpdateModelMsg        = model.UpdateModelMsg
	ModelsListMsg         = model.ModelsListMsg
	ModelsErrorMsg        = model.ModelsErrorMsg
	SettingsSaveResultMsg = model.SettingsSaveResultMsg
	SelectionTickMsg      = model.SelectionTickMsg
	HistoryMessagesMsg    = model.HistoryMessagesMsg
)
