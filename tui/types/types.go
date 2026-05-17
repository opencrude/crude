package types

import (
	"crude/sessiondb"
	"time"
)

const (
	ToolPreviewPreparing = "preparing"
	ToolPreviewStreaming = "streaming"
	ToolPreviewDone      = "done"
	ToolPreviewError     = "error"
)

const (
	RoleUser           = "user"
	RoleAssistant      = "assistant"
	RoleThinking       = "thinking"
	RoleTool           = "tool"
	RoleError          = "error"
	RoleSessionPicker  = "sessionpicker"
)

const (
	ConfirmAllow  = "allow"
	ConfirmAlways = "always"
	ConfirmDeny   = "deny"
)

type Settings struct {
	APIKey                   string
	Model                    string
	ModelContextWindow       int
	MainInputPrice           float64
	MainOutputPrice          float64
	MainCacheReadPrice       float64
	ProviderID               string
	ProviderURL              string
	ProviderAPIKey           string
	ThinkingEffort           string
	TurnLogsEnabled          string
}

type ModelEntry struct {
	ID             string
	Name           string
	ContextWindow  int
	InputPrice     float64
	OutputPrice    float64
	CacheReadPrice float64
}

type ProviderConfig struct {
	APIKey         string
	Model          string
	ProviderURL    string
	ThinkingEffort string
}

type ProviderPreset struct {
	ID       string
	Name     string
	HelpText string
	BaseURL  string
}

type ConfirmResult struct {
	Choice    string
	Operation string
	Tool      string
	Target    string
}

type UndoResultMsg struct {
	Desc  string
	Error string
}

type ChatMessage struct {
	InternalID string
	Role       string
	Content    string
	Time       time.Time

	ToolName   string
	ToolArgs   string
	ToolID     string
	ToolStatus string
	ToolIndex  int

	ToolProgressPhase   string
	ToolProgressMessage string
	ToolProgressLabel   string

	ToolPreviewPhase string
	ToolEstTokens    int
	ToolLivedAdded   int
	ToolLivedRemoved int
	ToolStartTime    time.Time
	ToolDuration     time.Duration

	ThinkingActive bool

	SubPaneID string

	// PermTag carries a compact permission indicator shown as a muted suffix
	// on tool message title lines. Values: "", "allowed", "always", "auto", "denied".
	PermTag string

	CachedLines          []string
	CursorLine           int
	StreamCompletedLines []string
	StreamCompletedLen   int

	SessionPickerSessions []sessiondb.SessionMeta
	SessionPickerSelected int
	SessionPickerFocused  bool
}

type AgentEventMsg struct {
	Type    string
	Data    any
	AgentID string
}

type TickMsg time.Time

type StatusMsg string

type ErrorMsg string

type AgentMsg AgentEventMsg

type OpenSettingsMsg struct{}

type FetchModelsMsg struct {
	ProviderID string
	BaseURL    string
	APIKey     string
}

type SaveSettingsMsg Settings

type UpdateModelMsg string

type ModelsListMsg []ModelEntry

type ModelsErrorMsg string

type SettingsSaveResultMsg struct {
	Settings Settings
	Err      error
}

type SelectionTickMsg time.Time
