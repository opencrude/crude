package types

import (
	"time"

	"crude/sessiondb"
)

const (
	ToolPreviewPreparing = "preparing"
	ToolPreviewStreaming = "streaming"
	ToolPreviewDone      = "done"
	ToolPreviewError     = "error"
)

const (
	ToolStatusCalling   = "calling"
	ToolStatusStreaming = "streaming"
	ToolStatusSuccess   = "success"
	ToolStatusError     = "error"
	ToolStatusCancelled = "cancelled"
)

const (
	RoleUser          = "user"
	RoleAssistant     = "assistant"
	RoleThinking      = "thinking"
	RoleTool          = "tool"
	RoleError         = "error"
	RoleSessionPicker = "sessionpicker"
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
	MultiAgent string
	TurnLogs   string
}

type AgentsSettings struct {
	Enabled       bool
	ActiveProfile string
	MaxConcurrent int
	Categories    map[string]CategorySettings
	Profiles      []ProfileSettings
}

type Settings struct {
	APIKey           string
	Model            string
	ProviderID       string
	ProviderURL      string
	ProviderAPIKey   string
	ThinkingEffort   string
	TurnLogsEnabled  string
	Agents           AgentsSettings
	SessionOverrides map[string]CategorySettings `json:"-"`
	Theme            ThemeSettings               `json:"theme,omitempty"`
	Editor           EditorSettings              `json:"editor,omitempty"`
	LSP              LSPSettings                 `json:"lsp,omitempty"`
	UI               UISettings                  `json:"ui,omitempty"`
	Font             FontSettings                `json:"font,omitempty"`
}

type AppSettings struct {
	Theme          ThemeSettings             `json:"theme,omitempty"`
	Editor         EditorSettings            `json:"editor,omitempty"`
	LSP            LSPSettings               `json:"lsp,omitempty"`
	UI             UISettings                `json:"ui,omitempty"`
	Font           FontSettings              `json:"font,omitempty"`
	ActiveProvider string                    `json:"active_provider,omitempty"`
	Providers      map[string]ProviderConfig `json:"providers,omitempty"`
	Agents         AgentsSettings            `json:"agents,omitempty"`
}

type SessionSettings struct {
	APIKey          string         `json:"api_key,omitempty"`
	Model           string         `json:"model,omitempty"`
	ProviderID      string         `json:"provider_id,omitempty"`
	ProviderURL     string         `json:"provider_url,omitempty"`
	ProviderAPIKey  string         `json:"provider_api_key,omitempty"`
	ThinkingEffort  string         `json:"thinking_effort,omitempty"`
	TurnLogsEnabled string         `json:"turn_logs_enabled,omitempty"`
	Agents          AgentsSettings `json:"agents,omitempty"`
}

func (s Settings) AppPart() AppSettings {
	return AppSettings{
		Theme:  s.Theme,
		Editor: s.Editor,
		LSP:    s.LSP,
		UI:     s.UI,
		Font:   s.Font,
	}
}

func (s Settings) SessionPart() SessionSettings {
	return SessionSettings{
		APIKey:          s.APIKey,
		Model:           s.Model,
		ProviderID:      s.ProviderID,
		ProviderURL:     s.ProviderURL,
		ProviderAPIKey:  s.ProviderAPIKey,
		ThinkingEffort:  s.ThinkingEffort,
		TurnLogsEnabled: s.TurnLogsEnabled,
		Agents:          s.Agents,
	}
}

type ThemeSettings struct {
	Preset    string `json:"preset,omitempty"`
	Bg        string `json:"bg,omitempty"`
	Fg        string `json:"fg,omitempty"`
	Accent    string `json:"accent,omitempty"`
	Accent2   string `json:"accent2,omitempty"`
	SubPaneBg string `json:"subpane_bg,omitempty"`
	Border    string `json:"border,omitempty"`
	Selection string `json:"selection,omitempty"`
}

type EditorSettings struct {
	TabSize          int    `json:"tab_size,omitempty"`
	WordWrap         bool   `json:"word_wrap,omitempty"`
	LineNumbers      bool   `json:"line_numbers,omitempty"`
	SyntaxTheme      string `json:"syntax_theme,omitempty"`
	Minimap          bool   `json:"minimap,omitempty"`
	AutoSaveDelay    int    `json:"auto_save_delay,omitempty"`
	RenderWhitespace string `json:"render_whitespace,omitempty"`
	FormatOnSave     bool   `json:"format_on_save,omitempty"`
}

// LSPLanguageSettings is the per-language overlay for one language server.
// Empty Command/Args inherit the built-in default for that language.
type LSPLanguageSettings struct {
	Enabled bool     `json:"enabled"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

// LSPSettings is the language-intelligence configuration. Bools are persisted
// without omitempty so an explicit off is distinct from "use defaults".
type LSPSettings struct {
	Enabled      bool `json:"enabled"`
	AutoDetect   bool `json:"auto_detect"`
	StartOnOpen  bool `json:"start_on_open"`
	Diagnostics  bool `json:"diagnostics"`
	Completions  bool `json:"completions"`
	Hover        bool `json:"hover"`
	Definition   bool `json:"goto_definition"`
	References   bool `json:"find_references"`
	Outline      bool `json:"document_outline"`
	Rename       bool `json:"rename"`
	DiagInEditor bool `json:"diag_in_editor"`
	DiagInTree   bool `json:"diag_in_tree"`
	DiagInStatus bool `json:"diag_in_status"`
	AutoComplete bool `json:"auto_complete"`
	// MinSeverity is error, warning, info, or hint.
	MinSeverity      string                         `json:"min_severity,omitempty"`
	UpdateDebounceMS int                            `json:"update_debounce_ms,omitempty"`
	Servers          map[string]string              `json:"servers,omitempty"`
	ServerArgs       map[string][]string            `json:"server_args,omitempty"`
	Languages        map[string]LSPLanguageSettings `json:"languages,omitempty"`
}

type UISettings struct {
	SidebarWidth      int  `json:"sidebar_width,omitempty"`
	ShowHiddenFiles   bool `json:"show_hidden_files,omitempty"`
	ShowIcons         bool `json:"show_icons,omitempty"`
	UseNerdFont       bool `json:"use_nerd_font,omitempty"`
	ShowGitStatus     bool `json:"show_git_status,omitempty"`
	ShowDiagnostics   bool `json:"show_diagnostics,omitempty"`
	CompactMode       bool `json:"compact_mode,omitempty"`
	MessageDelay      int  `json:"message_delay,omitempty"`
	AnimationsEnabled bool `json:"animations_enabled,omitempty"`
}

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
	APIKey         string `json:"api_key,omitempty"`
	ProviderURL    string `json:"provider_url,omitempty"`
	Model          string `json:"model,omitempty"`
	ThinkingEffort string `json:"thinking_effort,omitempty"`
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
	ToolPreviewPhase    string
	ToolEstTokens       int
	ToolLivedAdded      int
	ToolLivedRemoved    int
	ToolStartTime       time.Time
	ToolDuration        time.Duration

	ThinkingActive      bool
	ThinkingInterrupted bool

	Incomplete       bool
	IncompleteReason string

	SubPaneID string

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

// ConfigReloadedMsg is emitted when ~/.crude/config.json changes on disk so
// the TUI can hot-reload live preferences such as the theme preset.
type ConfigReloadedMsg struct {
	ThemePreset string
}

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
