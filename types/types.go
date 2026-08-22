package types

import (
	"time"

	"crude/provider"
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
	Provider    string
	ProviderURL string
	// APIKey is never a category property. Credentials live on the
	// provider; json:"-" keeps them out of workspace overlays.
	APIKey         string `json:"-"`
	APIStandard    string
	Model          string
	ThinkingEffort string
	// Inherit is the session-level live-inheritance marker: "main" follows
	// this session's main agent, "workspace" follows the workspace's main
	// agent. Empty means an explicit (manual) category. Never set on
	// workspace/app agent categories.
	Inherit             string `json:"inherit,omitempty"`
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
	APIStandard      string
	ThinkingEffort   string
	TurnLogsEnabled  string
	Agents           AgentsSettings
	Memory           MemorySettings              `json:"memory,omitempty"`
	SessionOverrides map[string]CategorySettings `json:"-"`
	// SessionMultiAgent is the explicit session override for sub-agent
	// spawning. nil means inherit the workspace/profile default.
	SessionMultiAgent *bool          `json:"-"`
	Theme             ThemeSettings  `json:"theme,omitempty"`
	Editor            EditorSettings `json:"editor,omitempty"`
	LSP               LSPSettings    `json:"lsp,omitempty"`
	UI                UISettings     `json:"ui,omitempty"`
	Font              FontSettings   `json:"font,omitempty"`
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
	Memory         MemorySettings            `json:"memory,omitempty"`
}

// Memory system defaults (applied by ApplyAppDefaults and used as fallbacks
// wherever a MemorySettings value is unset).
const (
	DefaultMemoryRecallBudget   = 1500
	DefaultMemoryRecallLimit    = 10
	DefaultMemoryExtractCadence = 3
)

// MemorySettings configures the workspace memory system. Tri-state strings
// ("on"/"off"/"" = inherit-or-default) follow the TurnLogsEnabled style so
// the sparse workspace-override diffing works unchanged.
type MemorySettings struct {
	// AutoRecall injects relevant memories into the system prompt each
	// turn. Default on.
	AutoRecall string `json:"auto_recall,omitempty"`
	// AutoExtract runs background LLM extraction of durable memories from
	// finished turns. Default on.
	AutoExtract string `json:"auto_extract,omitempty"`
	// Consolidation runs the background dedup/contradiction/decay job.
	// Default on.
	Consolidation string `json:"consolidation,omitempty"`
	// RecallBudgetTokens caps the auto-injected memory block. Default 1500.
	RecallBudgetTokens int `json:"recall_budget_tokens,omitempty"`
	// RecallLimit caps the number of auto-injected memories. Default 10.
	RecallLimit int `json:"recall_limit,omitempty"`
	// ExtractEveryTurns is the extraction cadence. Default 3.
	ExtractEveryTurns int `json:"extract_every_turns,omitempty"`
	// Model optionally overrides the model used for background memory jobs
	// (extraction, summaries, consolidation). Empty uses the session model.
	// Kept for backward-compat; prefer MemoryCategory.
	Model string `json:"model,omitempty"`
	// MemoryCategory is the memory agent's own configuration: provider,
	// model, and thinking effort. Empty fields inherit the main agent, so the
	// memory agent behaves like every other agent. It is set on the session
	// config and mirrored here for the settings form.
	MemoryCategory CategorySettings `json:"memory_category,omitempty"`
}

// AutoRecallEnabled reports whether recall injection is active (default on).
func (m MemorySettings) AutoRecallEnabled() bool { return m.AutoRecall != "off" }

// AutoExtractEnabled reports whether background extraction is active
// (default on).
func (m MemorySettings) AutoExtractEnabled() bool { return m.AutoExtract != "off" }

// ConsolidationEnabled reports whether background consolidation is active
// (default on).
func (m MemorySettings) ConsolidationEnabled() bool { return m.Consolidation != "off" }

// RecallBudgetOrDefault returns the configured budget or fallback.
func (m MemorySettings) RecallBudgetOrDefault(fallback int) int {
	if m.RecallBudgetTokens > 0 {
		return m.RecallBudgetTokens
	}
	return fallback
}

// RecallLimitOrDefault returns the configured limit or fallback.
func (m MemorySettings) RecallLimitOrDefault(fallback int) int {
	if m.RecallLimit > 0 {
		return m.RecallLimit
	}
	return fallback
}

// ExtractCadenceOrDefault returns the configured cadence or fallback.
func (m MemorySettings) ExtractCadenceOrDefault(fallback int) int {
	if m.ExtractEveryTurns > 0 {
		return m.ExtractEveryTurns
	}
	return fallback
}

type SessionSettings struct {
	APIKey          string         `json:"api_key,omitempty"`
	Model           string         `json:"model,omitempty"`
	ProviderID      string         `json:"provider_id,omitempty"`
	ProviderURL     string         `json:"provider_url,omitempty"`
	ProviderAPIKey  string         `json:"provider_api_key,omitempty"`
	APIStandard     string         `json:"api_standard,omitempty"`
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
		APIStandard:     s.APIStandard,
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
	APIKey         string                     `json:"api_key,omitempty"`
	ProviderURL    string                     `json:"provider_url,omitempty"`
	APIStandard    string                     `json:"api_standard,omitempty"`
	Model          string                     `json:"model,omitempty"`
	ModelStandards map[string]string          `json:"model_standards,omitempty"`
	ThinkingEffort string                     `json:"thinking_effort,omitempty"`
	OAuth          *provider.OAuthCredentials `json:"oauth,omitempty"`
}

type ProviderPreset struct {
	ID         string
	Name       string
	HelpText   string
	BaseURL    string
	StandardID string
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
	ThinkingDuration    time.Duration

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
