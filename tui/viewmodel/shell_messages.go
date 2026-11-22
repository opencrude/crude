package viewmodel

import (
	"crude/types/conversation"
	"crude/types/setting"
	"time"
)

// AgentEventMsg carries one agent event into the shell.
type AgentEventMsg struct {
	Type    string
	Data    any
	AgentID string
}

// TickMsg is one tick of the shell clock.
type TickMsg time.Time

// StatusMsg is a short status line for the footer.
type StatusMsg string

// ErrorMsg is a failure that the shell shows to the user.
type ErrorMsg string

// AgentMsg carries an agent event that is addressed to one agent.
type AgentMsg AgentEventMsg

// OpenSettingsMsg asks the shell to open the settings window.
type OpenSettingsMsg struct{}

// ConfigReloadedMsg is emitted when ~/.crude/config.json changes on disk so
// the TUI can hot-reload live preferences such as the theme preset.
type ConfigReloadedMsg struct {
	ThemePreset string
}

// FetchModelsMsg asks for the models of an account.
type FetchModelsMsg struct {
	ProviderID string
	AccountID  string
	BaseURL    string
}

// SaveSettingsMsg asks the shell to save settings.
type SaveSettingsMsg setting.Settings

// UpdateModelMsg asks the shell to select a model name.
type UpdateModelMsg string

// ModelsListMsg carries the models that an account offers.
type ModelsListMsg []setting.ModelEntry

// ModelsErrorMsg carries a failure to fetch models.
type ModelsErrorMsg string

// HistoryMessagesMsg carries stored transcript messages into the shell.
type HistoryMessagesMsg []conversation.ChatMessage
