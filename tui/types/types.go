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
	ToolStatusCalling    = "calling"
	ToolStatusStreaming  = "streaming"
	ToolStatusSuccess    = "success"
	ToolStatusError      = "error"
	ToolStatusCancelled  = "cancelled"
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

type CategorySettings struct {
	Provider       string
	ProviderURL    string
	APIKey         string
	Model          string
	ThinkingEffort string
	Policy         string
	MaxSteps       int
	TimeoutSeconds int
	Description    string
}

type ProfileSettings struct {
	Name       string
	Categories map[string]CategorySettings
	MultiAgent string // "on", "off", ""
	TurnLogs   string // "on", "off", ""
}

type AgentsSettings struct {
	Enabled       bool
	ActiveProfile string
	MaxConcurrent int
	MaxDepth      int
	Categories    map[string]CategorySettings
	Profiles      []ProfileSettings
}

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
	Agents                   AgentsSettings
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

type QuestionResult struct {
	Answer    string
	Custom    bool
	Cancelled bool
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

	ThinkingActive      bool
	ThinkingInterrupted bool

	Incomplete       bool
	IncompleteReason string

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

type HistoryMessagesMsg []ChatMessage
