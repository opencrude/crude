package types

import "time"

const (
	ToolPreviewPreparing = "preparing"
	ToolPreviewStreaming = "streaming"
	ToolPreviewDone      = "done"
	ToolPreviewError     = "error"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleThinking  = "thinking"
	RoleTool      = "tool"
	RoleError     = "error"
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
	OracleModel              string
	OracleModelContextWindow int
	OracleInputPrice         float64
	OracleOutputPrice        float64
	OracleCacheReadPrice     float64
	ProviderID               string
	ProviderURL              string
	ProviderAPIKey           string
	ThinkingEffort           string
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
	OracleModel    string
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
	Role    string
	Content string
	Time    time.Time

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

	CachedLines          []string
	CursorLine           int
	StreamCompletedLines []string
	StreamCompletedLen   int
}

type AgentEventMsg struct {
	Type string
	Data any
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

type OracleModelsListMsg []ModelEntry

type OracleModelsErrorMsg string

type SelectionTickMsg time.Time

type ClipboardCopiedMsg struct{}

type ClipboardErrorMsg string
