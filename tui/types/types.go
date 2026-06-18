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
	Provider            string
	ProviderURL         string
	APIKey              string
	Model               string
	ThinkingEffort      string
	Policy              string
	MaxSteps            int
	Description         string
	ModelContextWindow  int
	ModelInputPrice     float64
	ModelOutputPrice    float64
	ModelCachePrice     float64
	ModelSupportsVision bool
	ModelSupportsTools  bool
	ModelMaxOutput      int
	ModelAttachment     bool
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
	Categories    map[string]CategorySettings
	Profiles      []ProfileSettings
}

type Settings struct {
	APIKey          string
	Model           string
	ProviderID      string
	ProviderURL     string
	ProviderAPIKey  string
	ThinkingEffort  string
	TurnLogsEnabled string
	Agents          AgentsSettings
	Theme           ThemeSettings       `json:"theme,omitempty"`
	Editor          EditorSettings      `json:"editor,omitempty"`
	LSP             LSPSettings         `json:"lsp,omitempty"`
	UI              UISettings          `json:"ui,omitempty"`
	Font            FontSettings        `json:"font,omitempty"`
}

// ThemeSettings controls the visual appearance of the TUI.
type ThemeSettings struct {
	Preset     string `json:"preset,omitempty"`      // "dark", "light", "custom"
	Bg         string `json:"bg,omitempty"`          // hex color
	Fg         string `json:"fg,omitempty"`          // hex color
	Accent     string `json:"accent,omitempty"`      // hex color
	Accent2    string `json:"accent2,omitempty"`     // hex color
	SubPaneBg  string `json:"subpane_bg,omitempty"`  // hex color
	Border     string `json:"border,omitempty"`      // hex color
	Selection  string `json:"selection,omitempty"`   // hex color
}

// EditorSettings controls the built-in text editor.
type EditorSettings struct {
	TabSize       int    `json:"tab_size,omitempty"`
	WordWrap      bool   `json:"word_wrap,omitempty"`
	LineNumbers   bool   `json:"line_numbers,omitempty"`
	SyntaxTheme   string `json:"syntax_theme,omitempty"`   // "catppuccin", "dracula", "github", etc.
	Minimap       bool   `json:"minimap,omitempty"`
	AutoSaveDelay int    `json:"auto_save_delay,omitempty"` // ms, 0 = disabled
	RenderWhitespace string `json:"render_whitespace,omitempty"` // "none", "boundary", "all"
}

// LSPSettings controls the Language Server Protocol integration.
type LSPSettings struct {
	Enabled     bool                `json:"enabled,omitempty"`
	AutoDetect  bool                `json:"auto_detect,omitempty"`
	Servers     map[string]string   `json:"servers,omitempty"`      // language -> server command
	ServerArgs  map[string][]string `json:"server_args,omitempty"`  // language -> server args
	Diagnostics bool                `json:"diagnostics,omitempty"`
}

// UISettings controls general interface appearance and behavior.
type UISettings struct {
	SidebarWidth      int  `json:"sidebar_width,omitempty"`
	ShowHiddenFiles   bool `json:"show_hidden_files,omitempty"`
	ShowIcons         bool `json:"show_icons,omitempty"`
	UseNerdFont       bool `json:"use_nerd_font,omitempty"`
	ShowGitStatus     bool `json:"show_git_status,omitempty"`
	ShowDiagnostics   bool `json:"show_diagnostics,omitempty"`
	CompactMode       bool `json:"compact_mode,omitempty"`
	MessageDelay      int  `json:"message_delay,omitempty"` // ms
	AnimationsEnabled bool `json:"animations_enabled,omitempty"`
}

// FontSettings controls terminal font rendering hints.
type FontSettings struct {
	Family    string `json:"family,omitempty"`
	Size      int    `json:"size,omitempty"`
	Ligatures bool   `json:"ligatures,omitempty"`
}

type ModelEntry struct {
	ID             string
	Name           string
	ContextWindow  int
	InputPrice     float64
	OutputPrice    float64
	CacheReadPrice float64
	SupportsVision bool
	SupportsTools  bool
	MaxOutput      int
	Attachment     bool
}

type ProviderConfig struct {
	APIKey      string
	ProviderURL string
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
	Answers   map[string]string
	Questions map[string]string
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
