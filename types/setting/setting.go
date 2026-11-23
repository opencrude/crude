package setting

import (
	captypes "crude/types/capability"
	"crude/types/jsondoc"
	"maps"

	"crude/agent/provider"
	"crude/internal/network"
	"crude/text/textencoding"
)

// Category is the model configuration of one agent role.
type Category struct {
	Enabled     *bool `json:"enabled,omitempty"` // nil uses the built-in enabled state
	Provider    string
	AccountID   string `json:"account_id,omitempty"`
	ProviderURL string
	// APIKey is never a category property. Credentials live on accounts;
	// json:"-" keeps them out of workspace and session snapshots.
	APIKey         string `json:"-"`
	APIStandard    string
	Model          string
	ThinkingEffort string
	// Inherit is the session-level live-inheritance marker: "main" follows
	// this session's main agent, "workspace" follows the workspace's main
	// agent. Empty means an explicit (manual) category. Never set on
	// workspace/app agent categories.
	Inherit            string `json:"inherit,omitempty"`
	Policy             string
	Description        string
	Instructions       string
	AllowedTools       []string
	DeniedTools        []string
	SkillModes         map[string]string `json:"skill_modes,omitempty"`
	ModelContextWindow int
	ModelInputPrice    float64
	ModelOutputPrice   float64
	ModelCachePrice    float64
	// Modalities lists the input types beyond text the model accepts
	// (provider.ModalityImage, ModalityPDF, ModalityVideo, ModalityAudio).
	ModelModalities    []string
	ModelSupportsTools bool
	ModelMaxOutput     int
	// Sampling overrides; nil leaves the backend default in effect.
	ModelTemperature *float64
	ModelTopP        *float64
	// ContextCompactionPercent starts an automatic context compaction when the
	// agent reaches this percentage of its effective model window. nil lets
	// Crude decide from cost and capacity; 0 disables automatic compaction.
	ContextCompactionPercent *int `json:"context_compaction_percent,omitempty"`
	// ContextRetentionTokens bounds older verbatim input retained across
	// compaction. nil sizes it adaptively.
	ContextRetentionTokens *int `json:"context_retention_tokens,omitempty"`
	// ServiceTier selects this role's processing tier ("standard", "fast",
	// "priority", "flex", "ultrafast"); empty uses the account's setting. A tier the
	// connection's protocol does not offer is ignored.
	ServiceTier string `json:"service_tier,omitempty"`
}

// Agents holds the agent settings of the application, a workspace or a session.
type Agents struct {
	Enabled       bool
	ActiveProfile string
	MaxConcurrent int
	Categories    map[string]Category
	AgentProfiles map[string]AgentProfile `json:"agent_profiles,omitempty"`
	// Shell is the agent command shell: AgentShellAuto (or empty) or a
	// detected TerminalShell ID. It applies to every agent's commands.
	Shell string `json:"shell,omitempty"`
	// MaxWaitMinutes bounds every agent wait (wait, agent_wait); zero is
	// Auto (DefaultMaxWaitMinutes). A longer request ends at the bound and
	// the agent may wait again.
	MaxWaitMinutes int `json:"max_wait_minutes,omitempty"`
	// Suggestions is the connection for Errand's editor suggestions. It is
	// application-wide; each workspace keeps its own mode and hourly cap.
	Suggestions Suggestion `json:"suggestions,omitempty"`
}

// DefaultMaxWaitMinutes is the Auto bound on one agent wait; MaxWaitMinutesLimit
// is the largest value Settings accepts.
const (
	DefaultMaxWaitMinutes = 10
	MaxWaitMinutesLimit   = 240
)

// AgentProfileSettings is a named, reusable specialization of a built-in role.
type AgentProfile struct {
	Enabled                  *bool             `json:"enabled,omitempty"`
	Category                 string            `json:"category,omitempty"`
	Description              string            `json:"description,omitempty"`
	Model                    string            `json:"model,omitempty"`
	AccountID                string            `json:"account_id,omitempty"`
	Instructions             string            `json:"instructions,omitempty"`
	AllowedTools             []string          `json:"allowed_tools,omitempty"`
	DeniedTools              []string          `json:"denied_tools,omitempty"`
	SkillModes               map[string]string `json:"skill_modes,omitempty"`
	FileOwnership            []string          `json:"file_ownership,omitempty"`
	ContextCompactionPercent *int              `json:"context_compaction_percent,omitempty"`
	ContextRetentionTokens   *int              `json:"context_retention_tokens,omitempty"`
	ThinkingEffort           string            `json:"thinking_effort,omitempty"`
	// ServiceTier overrides the underlying role's tier when set.
	ServiceTier        string   `json:"service_tier,omitempty"`
	ModelContextWindow int      `json:"model_context_window,omitempty"`
	ModelMaxOutput     int      `json:"model_max_output,omitempty"`
	ModelModalities    []string `json:"model_modalities"`
	ModelTemperature   *float64 `json:"model_temperature,omitempty"`
	ModelTopP          *float64 `json:"model_top_p,omitempty"`
}

// DefaultAgentRoleCategories is the single reset/initialization source for
// shipped roles. Memory inherits the main connection unless explicitly edited.
func DefaultAgentRoleCategories() map[string]Category {
	return map[string]Category{
		"default": {},
		"researcher": {
			Policy: "read-only", Description: "Gatherer - explores codebase, searches web, runs experiments",
			AllowedTools: []string{"read_file", "glob_files", "grep_files", "list_directory", "web_search", "web_fetch", "arxiv_search", "publish_report", "question"},
			DeniedTools:  []string{"write_file", "edit_file", "delete_file", "move_file", "copy_file", "execute_command", "async_execute_command"},
		},
		"crafter": {Policy: "no-delete", Description: "Implementer - writes code, edits files, runs tests", DeniedTools: []string{"delete_file"}},
		// Errand is the workspace agent that installs and configures tools. It
		// keeps the Main Agent's full tool set; agent.CategoryToolDefaults and
		// agent.categoryPolicy say the same, and the user's permission mode is
		// its only limit.
		"errand": {Description: "Workspace agent - sets up tools and runs project errands"},
		"memory": {},
	}
}

// CloneAgentProfiles returns a deep copy of the agent profiles.
func CloneAgentProfiles(source map[string]AgentProfile) map[string]AgentProfile {
	if source == nil {
		return nil
	}
	out := make(map[string]AgentProfile, len(source))
	for key, value := range source {
		if value.Enabled != nil {
			enabled := *value.Enabled
			value.Enabled = &enabled
		}
		value.AllowedTools = append([]string(nil), value.AllowedTools...)
		value.DeniedTools = append([]string(nil), value.DeniedTools...)
		value.SkillModes = maps.Clone(value.SkillModes)
		value.FileOwnership = append([]string(nil), value.FileOwnership...)
		if value.ModelModalities != nil {
			value.ModelModalities = append(make([]string, 0, len(value.ModelModalities)), value.ModelModalities...)
		}
		if value.ModelTemperature != nil {
			temperature := *value.ModelTemperature
			value.ModelTemperature = &temperature
		}
		if value.ModelTopP != nil {
			topP := *value.ModelTopP
			value.ModelTopP = &topP
		}
		if value.ContextCompactionPercent != nil {
			n := *value.ContextCompactionPercent
			value.ContextCompactionPercent = &n
		}
		if value.ContextRetentionTokens != nil {
			n := *value.ContextRetentionTokens
			value.ContextRetentionTokens = &n
		}
		out[key] = value
	}
	return out
}

// FastMode describes the configured next-request tier, not a provider's
// reported serving tier. Unsupported means the selected protocol cannot send
// a retained fast choice; unavailable means its connection is missing or invalid.
type FastMode string

const (
	FastModeOff         FastMode = ""
	FastModeOn          FastMode = "on"
	FastModeUnsupported FastMode = "unsupported"
	FastModeUnavailable FastMode = "unavailable"
)

type Settings struct {
	// SessionEdits are addressed stored edits from the form's original layer.
	SessionEdits []Edit `json:"-"`
	// SessionMainAgent selects a built-in role or a named profile for root turns.
	// Empty selects the ordinary Main Agent; this is a session-only setting.
	SessionMainAgent string `json:"-"`
	APIKey           string
	Model            string
	ProviderID       string
	AccountID        string `json:"account_id,omitempty"`
	ProviderURL      string
	ProviderAPIKey   string
	APIStandard      string
	ThinkingEffort   string
	FastMode         FastMode `json:"fast_mode,omitempty"`
	TurnLogsEnabled  string
	Agents           Agents
	Memory           Memory              `json:"memory,omitempty"`
	Capabilities     captypes.Settings   `json:"capabilities,omitempty"`
	Safety           Safety              `json:"safety,omitempty"`
	LocalHistory     LocalHistory        `json:"local_history,omitempty"`
	SessionOverrides map[string]Category `json:"-"`
	// SessionMultiAgent is the explicit session override for sub-agent
	// spawning. nil means inherit the workspace/profile default.
	SessionMultiAgent *bool `json:"-"`
	// SessionCodeIntelligence lets this session's agents use language servers
	// independently of the editor; nil follows the workspace LSP switch.
	SessionCodeIntelligence *bool `json:"-"`
	// SessionMaxSubAgents caps this session's concurrently running sub-agents;
	// nil follows the workspace setting.
	SessionMaxSubAgents *int  `json:"-"`
	SessionTurnLogs     *bool `json:"-"`
	// SessionDisabledCapabilities lists installed skill/MCP IDs this session
	// and every agent it runs have turned off. Sorted; nil means none.
	SessionDisabledCapabilities []string `json:"-"`
	Theme                       Theme    `json:"theme,omitempty"`
	Editor                      Editor   `json:"editor,omitempty"`
	LSP                         LSP      `json:"lsp,omitempty"`
	UI                          UI       `json:"ui,omitempty"`
}

// SessionPreferences is the session's explicit layer, never its resolved view.
type SessionPreferences struct {
	MainAgent        string              `json:"main_agent"`
	Overrides        map[string]Category `json:"overrides"`
	MultiAgent       *bool               `json:"multi_agent"`
	CodeIntelligence *bool               `json:"code_intelligence"`
	MaxSubAgents     *int                `json:"max_sub_agents"`
	TurnLogs         *bool               `json:"turn_logs"`
}

func SessionPreferencesOf(s Settings) SessionPreferences {
	return SessionPreferences{MainAgent: s.SessionMainAgent, Overrides: s.SessionOverrides, MultiAgent: s.SessionMultiAgent,
		CodeIntelligence: s.SessionCodeIntelligence, MaxSubAgents: s.SessionMaxSubAgents, TurnLogs: s.SessionTurnLogs}
}

// App is the application settings that the shell loads and saves.
type App struct {
	Onboarding Onboarding `json:"onboarding,omitempty"`
	Theme      Theme      `json:"theme,omitempty"`
	Editor     Editor     `json:"editor,omitempty"`
	LSP        LSP        `json:"lsp,omitempty"`
	// Analysis is workspace-owned despite sharing this effective/schema carrier.
	Analysis WorkspaceAnalysis `json:"analysis,omitempty"`
	// Prediction is workspace-only egress authority, never an application/session default.
	Prediction   WorkspacePrediction         `json:"prediction,omitempty"`
	UI           UI                          `json:"ui,omitempty"`
	Accounts     map[string]provider.Account `json:"accounts,omitempty"`
	Agents       Agents                      `json:"agents,omitempty"`
	Memory       Memory                      `json:"memory,omitempty"`
	Capabilities captypes.Settings           `json:"capabilities,omitempty"`
	Safety       Safety                      `json:"safety,omitempty"`
	LocalHistory LocalHistory                `json:"local_history,omitempty"`
	Terminal     Terminal                    `json:"terminal,omitempty"`
	// Shells are observations on the core host, never persisted choices. A local
	// attachment may supply a different search environment for new terminals.
	TerminalShells []TerminalShell `json:"-"`
	AgentShells    []TerminalShell `json:"-"`
	// Network is application-owned: trust, proxy credentials and client
	// identities must never be promoted by a project's settings overlay.
	Network network.Settings `json:"network,omitempty"`
}

type CommandSeverityRule struct {
	Pattern string `json:"pattern"`
	Risk    string `json:"risk"`
}

// Safety holds the command confirmation and permission settings.
type Safety struct {
	ConfirmAt           string                `json:"confirm_at,omitempty"`
	UnknownCommandRisk  string                `json:"unknown_command_risk,omitempty"`
	ScriptExecutionRisk string                `json:"script_execution_risk,omitempty"`
	CommandRules        []CommandSeverityRule `json:"command_rules,omitempty"`
	NotifyDangerous     string                `json:"notify_dangerous,omitempty"`
	AutoPermission      AutoPermission        `json:"auto_permission,omitempty"`
}

// AutoPermissionSettings configures the permission reviewer used independently
// by each root and child. Provider and Model are deliberately explicit: an
// empty value means unconfigured rather than inheriting the acting model.
type AutoPermission struct {
	Enabled              string `json:"enabled,omitempty"`
	AccountID            string `json:"account_id,omitempty"`
	Provider             string `json:"provider,omitempty"`
	Model                string `json:"model,omitempty"`
	ModelType            string `json:"model_type,omitempty"`
	DenyMode             string `json:"deny_mode,omitempty"`
	UnresolvedMode       string `json:"unresolved_mode,omitempty"`
	Instructions         string `json:"instructions,omitempty"`
	ThinkingEffort       string `json:"thinking_effort,omitempty"`
	ServiceTier          string `json:"service_tier,omitempty"`
	ResponseFormat       string `json:"response_format,omitempty"`
	TimeoutSeconds       int    `json:"timeout_seconds,omitempty"`
	MaxOutputTokens      int    `json:"max_output_tokens,omitempty"`
	MaxAttempts          int    `json:"max_attempts,omitempty"`
	RetryTokenGrowth     string `json:"retry_token_growth,omitempty"`
	MaxRetryOutputTokens int    `json:"max_retry_output_tokens,omitempty"`
	MaxInputBytes        int    `json:"max_input_bytes,omitempty"`
}

const (
	AutoPermissionFormatAuto       = "auto"
	AutoPermissionFormatJSONSchema = "json-schema"
	AutoPermissionFormatJSONObject = "json-object"
	AutoPermissionFormatPrompt     = "prompt"

	AutoPermissionGrowthFixed = "fixed"
	AutoPermissionGrowth2x    = "2x"
	AutoPermissionGrowth4x    = "4x"
)

// Normalized returns safe effective values for the bounded permission
// reviewer. It is opt-in: only the exact value "on" enables remote review.
func (s AutoPermission) Normalized() AutoPermission {
	if s.Enabled == "on" {
		s.Enabled = "on"
	} else {
		s.Enabled = "off"
	}
	if s.DenyMode != "deny" {
		s.DenyMode = "ask"
	}
	if s.UnresolvedMode != "deny" {
		s.UnresolvedMode = "ask"
	}
	switch s.ThinkingEffort {
	case "low", "medium", "high", "max":
	default:
		s.ThinkingEffort = "low"
	}
	switch s.ServiceTier {
	case "", "standard", "fast", "priority", "flex", "ultrafast":
	default:
		s.ServiceTier = ""
	}
	if !ValidModelType(s.ModelType) {
		s.ModelType = ModelTypeAutomatic
	}
	switch s.ResponseFormat {
	case AutoPermissionFormatAuto, AutoPermissionFormatJSONSchema, AutoPermissionFormatJSONObject, AutoPermissionFormatPrompt:
	default:
		s.ResponseFormat = AutoPermissionFormatAuto
	}
	switch s.RetryTokenGrowth {
	case AutoPermissionGrowthFixed, AutoPermissionGrowth2x, AutoPermissionGrowth4x:
	default:
		s.RetryTokenGrowth = AutoPermissionGrowth4x
	}
	if s.TimeoutSeconds == 0 {
		s.TimeoutSeconds = 15
	}
	if s.MaxOutputTokens == 0 {
		s.MaxOutputTokens = 256
	}
	if s.MaxAttempts == 0 {
		s.MaxAttempts = 3
	}
	if s.MaxRetryOutputTokens == 0 {
		s.MaxRetryOutputTokens = 1024
	}
	if s.MaxInputBytes == 0 {
		s.MaxInputBytes = 64 << 10
	}
	s.TimeoutSeconds = min(120, max(1, s.TimeoutSeconds))
	s.MaxOutputTokens = min(4096, max(16, s.MaxOutputTokens))
	s.MaxAttempts = min(6, max(1, s.MaxAttempts))
	s.MaxRetryOutputTokens = min(8192, max(16, s.MaxRetryOutputTokens))
	if s.MaxRetryOutputTokens < s.MaxOutputTokens {
		s.MaxRetryOutputTokens = s.MaxOutputTokens
	}
	s.MaxInputBytes = min(1<<20, max(64<<10, s.MaxInputBytes))
	return s
}

// ReviewOutputTokens returns the configured output budget for a zero-based
// attempt. Growth is deterministic and always stops at the persisted ceiling.
func (s AutoPermission) ReviewOutputTokens(attempt int) int {
	s = s.Normalized()
	if attempt <= 0 || s.RetryTokenGrowth == AutoPermissionGrowthFixed {
		return s.MaxOutputTokens
	}
	multiplier := 2
	if s.RetryTokenGrowth == AutoPermissionGrowth4x {
		multiplier = 4
	}
	budget := s.MaxOutputTokens
	for range attempt {
		if budget >= s.MaxRetryOutputTokens/multiplier {
			return s.MaxRetryOutputTokens
		}
		budget *= multiplier
	}
	return min(budget, s.MaxRetryOutputTokens)
}

// LocalHistory holds the snapshot settings of local history.
type LocalHistory struct {
	Enabled                     string   `json:"enabled,omitempty"`
	UseGitIgnore                string   `json:"use_gitignore,omitempty"`
	ExtraIgnorePatterns         []string `json:"extra_ignore_patterns,omitempty"`
	MaxFileSizeMB               int      `json:"max_file_size_mb,omitempty"`
	DebounceMS                  int      `json:"debounce_ms,omitempty"`
	ReconcileIntervalSeconds    int      `json:"reconcile_interval_seconds,omitempty"`
	RetentionKeepHours          int      `json:"retention_keep_hours,omitempty"`
	RetentionKeepDaily          int      `json:"retention_keep_daily,omitempty"`
	RetentionMaxSnapshots       int      `json:"retention_max_snapshots,omitempty"`
	StorageBudgetMB             int      `json:"storage_budget_mb,omitempty"`
	CaptureExternalChanges      string   `json:"capture_external_changes,omitempty"`
	SafetySnapshotBeforeRestore string   `json:"safety_snapshot_before_restore,omitempty"`
}

// EnabledValue reports whether local history is on.
func (s LocalHistory) EnabledValue() bool { return s.Enabled != "off" }

// UseGitIgnoreValue reports whether history skips the files that Git ignores.
func (s LocalHistory) UseGitIgnoreValue() bool { return s.UseGitIgnore != "off" }

// CaptureExternalValue reports whether history records changes made outside the editor.
func (s LocalHistory) CaptureExternalValue() bool { return s.CaptureExternalChanges != "off" }

// SafetySnapshotValue reports whether a restore takes a safety snapshot first.
func (s LocalHistory) SafetySnapshotValue() bool {
	return s.SafetySnapshotBeforeRestore != "off"
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
type Memory struct {
	Ranker MemoryRanker `json:"ranker,omitzero"`
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
	// MemoryCategory configures the independent memory agent. Only a missing
	// account and model follow Errand; its instructions and request settings
	// remain its own.
	MemoryCategory Category `json:"memory_category,omitempty"`
}

// AutoRecallEnabled reports whether recall injection is active (default on).
func (m Memory) AutoRecallEnabled() bool { return m.AutoRecall != "off" }

// AutoExtractEnabled reports whether background extraction is active
// (default on).
func (m Memory) AutoExtractEnabled() bool { return m.AutoExtract != "off" }

// ConsolidationEnabled reports whether background consolidation is active
// (default on).
func (m Memory) ConsolidationEnabled() bool { return m.Consolidation != "off" }

// RecallBudgetOrDefault returns the configured budget or fallback.
func (m Memory) RecallBudgetOrDefault(fallback int) int {
	if m.RecallBudgetTokens > 0 {
		return m.RecallBudgetTokens
	}
	return fallback
}

// RecallLimitOrDefault returns the configured limit or fallback.
func (m Memory) RecallLimitOrDefault(fallback int) int {
	if m.RecallLimit > 0 {
		return m.RecallLimit
	}
	return fallback
}

// ExtractCadenceOrDefault returns the configured cadence or fallback.
func (m Memory) ExtractCadenceOrDefault(fallback int) int {
	if m.ExtractEveryTurns > 0 {
		return m.ExtractEveryTurns
	}
	return fallback
}

// Session holds the settings that one session can override.
type Session struct {
	APIKey          string `json:"-"`
	Model           string `json:"model,omitempty"`
	ProviderID      string `json:"provider_id,omitempty"`
	AccountID       string `json:"account_id,omitempty"`
	ProviderURL     string `json:"provider_url,omitempty"`
	ProviderAPIKey  string `json:"-"`
	APIStandard     string `json:"api_standard,omitempty"`
	ThinkingEffort  string `json:"thinking_effort,omitempty"`
	TurnLogsEnabled string `json:"turn_logs_enabled,omitempty"`
	Agents          Agents `json:"agents,omitempty"`
}

// AppPart returns the application part of the settings.
func (s Settings) AppPart() App {
	return App{
		Theme:        s.Theme,
		Editor:       s.Editor,
		LSP:          s.LSP,
		UI:           s.UI,
		LocalHistory: s.LocalHistory,
	}
}

// SessionPart returns the session part of the settings.
func (s Settings) SessionPart() Session {
	return Session{
		APIKey:          s.APIKey,
		Model:           s.Model,
		ProviderID:      s.ProviderID,
		AccountID:       s.AccountID,
		ProviderURL:     s.ProviderURL,
		ProviderAPIKey:  s.ProviderAPIKey,
		APIStandard:     s.APIStandard,
		ThinkingEffort:  s.ThinkingEffort,
		TurnLogsEnabled: s.TurnLogsEnabled,
		Agents:          s.Agents,
	}
}

// Theme holds the palette and the frame padding of the shell.
type Theme struct {
	Preset    string `json:"preset,omitempty"`
	Bg        string `json:"bg,omitempty"`
	Fg        string `json:"fg,omitempty"`
	Accent    string `json:"accent,omitempty"`
	Accent2   string `json:"accent2,omitempty"`
	SubPaneBg string `json:"subpane_bg,omitempty"`
	Border    string `json:"border,omitempty"`
	Selection string `json:"selection,omitempty"`
	// Frame paddings (in terminal cells) around the whole shell: top and
	// bottom reserve blank rows, left and right reserve blank columns.
	// Pointers so an unset field means "inherit the theme default" under the
	// sparse workspace-overlay merge; nil marshals as an absent key.
	PadTop    *int `json:"pad_top,omitempty"`
	PadBottom *int `json:"pad_bottom,omitempty"`
	PadLeft   *int `json:"pad_left,omitempty"`
	PadRight  *int `json:"pad_right,omitempty"`
}

// DefaultThemeFramePad is the shell frame padding for a fresh configuration:
// zero on every side. Padding is purely a user preference configured in
// Settings → Appearance; the shell renders edge to edge unless the user
// reserves blank cells.
const DefaultThemeFramePad = 0

// padWithDefault dereferences an optional frame padding. Unset or negative
// means "inherit", which resolves to the side's theme default.
func padWithDefault(p *int, dflt int) int {
	if p == nil || *p < 0 {
		return dflt
	}
	return *p
}

// PadTopOrDefault returns the effective top frame padding.
func (t Theme) PadTopOrDefault() int { return padWithDefault(t.PadTop, DefaultThemeFramePad) }

// PadBottomOrDefault returns the effective bottom frame padding.
func (t Theme) PadBottomOrDefault() int {
	return padWithDefault(t.PadBottom, DefaultThemeFramePad)
}

// PadLeftOrDefault returns the effective left frame padding.
func (t Theme) PadLeftOrDefault() int { return padWithDefault(t.PadLeft, DefaultThemeFramePad) }

// PadRightOrDefault returns the effective right frame padding.
func (t Theme) PadRightOrDefault() int {
	return padWithDefault(t.PadRight, DefaultThemeFramePad)
}

type AutoSaveMode string

const (
	AutoSaveOff               AutoSaveMode = "off"
	AutoSaveAfterDelay        AutoSaveMode = "after_delay"
	AutoSaveEditorFocusChange AutoSaveMode = "editor_focus_change"
	AutoSaveTerminalFocusLoss AutoSaveMode = "terminal_focus_loss"
)

// Editor holds the editor preferences.
type Editor struct {
	IndentStyle      string                `json:"indent_style,omitempty"`
	IndentSize       int                   `json:"indent_size,omitempty"`
	TabSize          int                   `json:"tab_size,omitempty"`
	WordWrap         bool                  `json:"word_wrap,omitempty"`
	LineNumbers      bool                  `json:"line_numbers,omitempty"`
	SyntaxTheme      string                `json:"syntax_theme,omitempty"`
	Minimap          bool                  `json:"minimap,omitempty"`
	IndentGuides     bool                  `json:"indent_guides,omitempty"`
	AutoSave         AutoSaveMode          `json:"auto_save"`
	AutoSaveDelay    int                   `json:"auto_save_delay,omitempty"`
	RenderWhitespace string                `json:"render_whitespace,omitempty"`
	FormatOnSave     bool                  `json:"format_on_save,omitempty"`
	NewFileEncoding  textencoding.Encoding `json:"new_file_encoding"`
	// TextDirection is EditorTextDirectionByLine (the default: each line
	// takes the direction of its prose; code stays left to right) or
	// EditorTextDirectionLTR (every line is a left-to-right paragraph).
	// Empty means by line. See docs/reference/bidi.md.
	TextDirection string `json:"text_direction,omitempty"`
	// MarkdownView is how a Markdown file opens: MarkdownViewPreview (the
	// default), MarkdownViewSplit or MarkdownViewSource. Each tab can change
	// it. See docs/reference/editor.md.
	MarkdownView string `json:"markdown_view,omitempty"`
}

// Markdown file views.
const (
	MarkdownViewPreview = "preview"
	MarkdownViewSplit   = "split"
	MarkdownViewSource  = "source"
)

// MarkdownViewOrDefault returns a supported Markdown view. An absent or
// unknown choice is MarkdownViewPreview.
func (s Editor) MarkdownViewOrDefault() string {
	switch s.MarkdownView {
	case MarkdownViewSplit, MarkdownViewSource:
		return s.MarkdownView
	}
	return MarkdownViewPreview
}

// Editor line directions.
const (
	EditorTextDirectionLTR    = "ltr"
	EditorTextDirectionByLine = "by_line"
)

// TextDirectionOrDefault returns a supported editor line direction. An
// absent choice is EditorTextDirectionByLine.
func (s Editor) TextDirectionOrDefault() string {
	if s.TextDirection == EditorTextDirectionLTR {
		return EditorTextDirectionLTR
	}
	return EditorTextDirectionByLine
}

// LSPLanguageSettings is the per-language overlay for one language server.
// The key is a catalog language ID or a custom language ID. Empty Command
// uses the language's first catalog server; a Command that matches another
// catalog server of the language selects that server and its defaults.
type LSPLanguage struct {
	Enabled               bool              `json:"enabled"`
	Command               string            `json:"command,omitempty"`
	Args                  []string          `json:"args,omitempty"`
	Extensions            []string          `json:"extensions,omitempty"`
	LanguageID            string            `json:"language_id,omitempty"`
	RootMarkers           []string          `json:"root_markers,omitempty"`
	Environment           map[string]string `json:"environment,omitempty"`
	Settings              jsondoc.Object    `json:"settings,omitempty"`
	InitializationOptions jsondoc.Object    `json:"initialization_options,omitempty"`
}

// LSPSettings is the language-intelligence configuration. Bools are persisted
// without omitempty so an explicit off is distinct from "use defaults".
type LSP struct {
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
	InlayHints   bool `json:"inlay_hints"`
	DiagInEditor bool `json:"diag_in_editor"`
	DiagInTree   bool `json:"diag_in_tree"`
	DiagInStatus bool `json:"diag_in_status"`
	AutoComplete bool `json:"auto_complete"`
	// A nil delay inherits the default; an explicit zero means no typing delay.
	CompletionDelayMS *int   `json:"completion_delay_ms,omitempty"`
	CompletionAccept  string `json:"completion_accept,omitempty"`
	// ProjectCheck is idle (check files that are not open while the user and
	// the computer are idle), always (also on battery and under load) or off.
	ProjectCheck string `json:"project_check,omitempty"`
	// AskErrand is on (the workspace Errand investigates a language-server
	// problem that Crude finds and asks the user before it changes anything)
	// or off. Only the user changes it.
	AskErrand string `json:"ask_errand,omitempty"`
	// MinSeverity is error, warning, info, or hint.
	MinSeverity        string                 `json:"min_severity,omitempty"`
	UpdateDebounceMS   int                    `json:"update_debounce_ms,omitempty"`
	ServerThreads      int                    `json:"server_threads,omitempty"`
	ServerMemoryMB     int                    `json:"server_memory_mb,omitempty"`
	MaxServers         int                    `json:"max_servers,omitempty"`
	IdleTimeoutSeconds int                    `json:"idle_timeout_seconds,omitempty"`
	MaxDocumentBytes   int                    `json:"max_document_bytes,omitempty"`
	Languages          map[string]LSPLanguage `json:"languages,omitempty"`
}

// CompletionDelay returns the completion delay in milliseconds, 200 by default.
func (s LSP) CompletionDelay() int {
	if s.CompletionDelayMS == nil {
		return 200
	}
	return min(60000, max(0, *s.CompletionDelayMS))
}

// Project check modes.
const (
	ProjectCheckIdle   = "idle"
	ProjectCheckAlways = "always"
	ProjectCheckOff    = "off"
)

// ProjectCheckMode says whether Crude checks files that are not open when
// their language server does not report them itself.
func (s LSP) ProjectCheckMode() string {
	switch s.ProjectCheck {
	case ProjectCheckOff, ProjectCheckAlways:
		return s.ProjectCheck
	}
	return ProjectCheckIdle
}

// ErrandInvestigates reports whether Crude asks the workspace Errand to
// investigate a language-server problem it finds. It is on unless the user
// turned it off.
func (s LSP) ErrandInvestigates() bool { return s.AskErrand != "off" }

// CompletionAcceptance is the key policy for a visible suggestion list only.
// Without that list, indentation, newlines and snippet navigation are unchanged.
func (s LSP) CompletionAcceptance() string {
	switch s.CompletionAccept {
	case "tab", "enter":
		return s.CompletionAccept
	default:
		return "tab-enter"
	}
}

const (
	AttachmentOpenSystem      = "system"
	AttachmentOpenFileManager = "file-manager"
	AttachmentOpenCrude       = "crude"
)

// DesktopNotificationSettings selects which agent news reaches the desktop
// while the user is away from Crude, and how. A nil switch is on.
type DesktopNotifications struct {
	Enabled *bool `json:"enabled,omitempty"`
	// Input covers questions and approvals that wait for the user.
	Input *bool `json:"input,omitempty"`
	// Stopped covers runs that stop with an error, a limit or an interruption.
	Stopped *bool `json:"stopped,omitempty"`
	// Finished covers turns of user-started agents that finish.
	Finished *bool `json:"finished,omitempty"`
	Sound    *bool `json:"sound,omitempty"`
	// AwayMinutes is how long a focused terminal may stay without input
	// before its user counts as away.
	AwayMinutes int `json:"away_minutes,omitempty"`
}

// Desktop notification away delay, in minutes.
const (
	DesktopNotificationAwayDefault = 5
	DesktopNotificationAwayMin     = 1
	DesktopNotificationAwayMax     = 60
)

func onUnlessOff(value *bool) bool { return value == nil || *value }

// EnabledOrDefault reports whether desktop notifications are on.
func (s DesktopNotifications) EnabledOrDefault() bool { return onUnlessOff(s.Enabled) }

// InputOrDefault reports whether questions and approvals notify.
func (s DesktopNotifications) InputOrDefault() bool { return onUnlessOff(s.Input) }

// StoppedOrDefault reports whether stopped runs notify.
func (s DesktopNotifications) StoppedOrDefault() bool { return onUnlessOff(s.Stopped) }

// FinishedOrDefault reports whether finished turns notify.
func (s DesktopNotifications) FinishedOrDefault() bool { return onUnlessOff(s.Finished) }

// SoundOrDefault reports whether a new message plays the system sound.
func (s DesktopNotifications) SoundOrDefault() bool { return onUnlessOff(s.Sound) }

// AwayMinutesOrDefault returns the away delay within its supported range.
func (s DesktopNotifications) AwayMinutesOrDefault() int {
	if s.AwayMinutes == 0 {
		return DesktopNotificationAwayDefault
	}
	return min(max(s.AwayMinutes, DesktopNotificationAwayMin), DesktopNotificationAwayMax)
}

// Active reports whether any agent news can reach the desktop.
func (s DesktopNotifications) Active() bool {
	return s.EnabledOrDefault() && (s.InputOrDefault() || s.StoppedOrDefault() || s.FinishedOrDefault())
}

// UI holds the appearance preferences of the shell.
type UI struct {
	SidebarWidth      int   `json:"sidebar_width,omitempty"`
	ShowHiddenFiles   *bool `json:"show_hidden_files,omitempty"`
	ShowIcons         bool  `json:"show_icons,omitempty"`
	UseNerdFont       bool  `json:"use_nerd_font,omitempty"`
	ShowGitStatus     bool  `json:"show_git_status,omitempty"`
	ShowDiagnostics   bool  `json:"show_diagnostics,omitempty"`
	CompactMode       bool  `json:"compact_mode,omitempty"`
	MessageDelay      int   `json:"message_delay,omitempty"`
	AnimationsEnabled bool  `json:"animations_enabled,omitempty"`
	// ContextHelp is compact, expanded, or off. Full Help remains available.
	ContextHelp string `json:"context_help,omitempty"`
	// AttachmentOpen selects the destination for opening chat attachments.
	AttachmentOpen string `json:"attachment_open,omitempty"`
	// DesktopNotifications selects which agent news reaches the desktop
	// while the user is away from Crude.
	DesktopNotifications DesktopNotifications `json:"desktop_notifications,omitzero"`
	// RightToLeft selects who arranges right-to-left text (Arabic, Persian,
	// Hebrew): automatic (the default: Crude with joined letters, unless the
	// terminal always reorders text itself), Crude with joined or separate
	// letters, or the terminal. See docs/reference/bidi.md.
	RightToLeft string `json:"right_to_left,omitempty"`
	// CaretMovement selects whether Left and Right move the caret on screen
	// (the default) or in text order inside right-to-left text.
	CaretMovement string `json:"caret_movement,omitempty"`
}

// Right-to-left text arrangement and caret movement choices.
const (
	RightToLeftAuto      = "auto"
	RightToLeftArrange   = "arrange"
	RightToLeftUnjoined  = "unjoined"
	RightToLeftTerminal  = "terminal"
	CaretMovementVisual  = "visual"
	CaretMovementLogical = "logical"
)

// RightToLeftOrDefault returns a supported arrangement choice. An absent
// choice is RightToLeftAuto.
func (s UI) RightToLeftOrDefault() string {
	switch s.RightToLeft {
	case RightToLeftArrange, RightToLeftUnjoined, RightToLeftTerminal:
		return s.RightToLeft
	default:
		return RightToLeftAuto
	}
}

// CaretMovementOrDefault returns a supported caret movement choice.
func (s UI) CaretMovementOrDefault() string {
	if s.CaretMovement == CaretMovementLogical {
		return CaretMovementLogical
	}
	return CaretMovementVisual
}

// ShowHiddenFilesOrDefault shows dotfiles unless the user explicitly hid them.
func (s UI) ShowHiddenFilesOrDefault() bool {
	return s.ShowHiddenFiles == nil || *s.ShowHiddenFiles
}

// AttachmentOpenOrDefault returns a supported attachment destination.
func (s UI) AttachmentOpenOrDefault() string {
	switch s.AttachmentOpen {
	case AttachmentOpenSystem, AttachmentOpenFileManager, AttachmentOpenCrude:
		return s.AttachmentOpen
	default:
		return AttachmentOpenSystem
	}
}

type ModelEntry struct {
	ID             string
	Name           string
	ContextWindow  int
	InputPrice     float64
	OutputPrice    float64
	CacheReadPrice float64
	// Modalities lists accepted input types beyond text.
	Modalities            []string
	SupportsTools         bool
	MaxOutput             int
	ModalitySupport       map[string]bool
	CapabilitySources     []string
	ConflictingModalities []string
	// Decision is true when the account's model list marks a typed decision
	// model (for example Ollama's "decision" capability).
	Decision bool
}

// ModelCatalogUpdatedMsg is silent metadata invalidation, not a notification.
type ModelCatalogUpdatedMsg struct{ Generation uint64 }

// ModelEntriesFromInfos converts provider model infos into UI model entries,
// enriching each with the cached public-source metadata (pricing, context
// window, capabilities). Context window and output limit from the wire win
// over the catalog.
func ModelEntriesFromInfos(models []provider.ModelInfo) []ModelEntry {
	entries := make([]ModelEntry, 0, len(models))
	for _, model := range models {
		entry := ModelEntry{
			ID: model.ID, Name: model.Name, ContextWindow: model.ContextWindow, MaxOutput: model.MaxOutput,
			Decision: model.Decision,
		}
		if meta, ok := provider.ModelCard(model.ID); ok {
			if entry.ContextWindow == 0 {
				entry.ContextWindow = meta.ContextWindow
			}
			entry.InputPrice = meta.InputPrice
			entry.OutputPrice = meta.OutputPrice
			entry.CacheReadPrice = meta.CacheReadPrice
			entry.Modalities = meta.Modalities
			entry.SupportsTools = meta.SupportsTools
			if entry.MaxOutput == 0 {
				entry.MaxOutput = meta.MaxOutput
			}
			entry.ModalitySupport = meta.ModalitySupport
			entry.CapabilitySources = meta.Sources
			entry.ConflictingModalities = meta.ConflictingModalities
		}
		entries = append(entries, entry)
	}
	return entries
}

type ProviderPreset struct {
	ID         string
	Name       string
	HelpText   string
	BaseURL    string
	StandardID string
}

// MaxConcurrentSubAgents bounds the configurable sub-agent limit. Every
// running child holds a model connection and tool processes.
const MaxConcurrentSubAgents = 32
