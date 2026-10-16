package types

import (
	captypes "crude/types/capability"
	"maps"
	"time"

	"crude/network"
	"crude/provider"
	"crude/types/reasoning"
	"crude/types/recovery"
)

// ConversationMessage is the presentation-neutral canonical message shared by
// live workspace state, durable session snapshots, and every frontend. Adding
// a lifecycle field here automatically includes it in JSON persistence; TUI
// rendering caches and selection state deliberately live elsewhere.
type ConversationMessage struct {
	InternalID      string                             `json:"internal_id,omitempty"`
	Role            string                             `json:"role"`
	Origin          provider.MessageOrigin             `json:"origin,omitempty"`
	Content         string                             `json:"content,omitempty"`
	ReferenceLabels []string                           `json:"reference_labels,omitempty"`
	Time            time.Time                          `json:"time,omitempty"`
	TurnGeneration  uint64                             `json:"turn_generation,omitempty"`
	ProviderID      string                             `json:"provider_id,omitempty"`
	AccountID       string                             `json:"account_id,omitempty"`
	ModelID         string                             `json:"model_id,omitempty"`
	Collaboration   *provider.CollaborationAttribution `json:"collaboration,omitempty"`

	ToolName            string        `json:"tool_name,omitempty"`
	ToolArgs            string        `json:"tool_args,omitempty"`
	ToolCommand         string        `json:"tool_command,omitempty"`
	ToolInput           string        `json:"tool_input,omitempty"`
	ToolScope           string        `json:"tool_scope,omitempty"`
	ToolID              string        `json:"tool_id,omitempty"`
	ToolRoundID         string        `json:"tool_round_id,omitempty"`
	ToolStatus          string        `json:"tool_status,omitempty"`
	ToolIndex           int           `json:"tool_index,omitempty"`
	ToolProgressPhase   string        `json:"tool_progress_phase,omitempty"`
	ToolProgressMessage string        `json:"tool_progress_message,omitempty"`
	ToolProgressLabel   string        `json:"tool_progress_label,omitempty"`
	ToolPreviewPhase    string        `json:"tool_preview_phase,omitempty"`
	ToolEstTokens       int           `json:"tool_est_tokens,omitempty"`
	ToolLinesAdded      int           `json:"tool_lines_added,omitempty"`
	ToolLinesRemoved    int           `json:"tool_lines_removed,omitempty"`
	ToolStartTime       time.Time     `json:"tool_start_time,omitempty"`
	ToolDuration        time.Duration `json:"tool_duration,omitempty"`

	ThinkingActive         bool              `json:"thinking_active,omitempty"`
	ThinkingInterrupted    bool              `json:"thinking_interrupted,omitempty"`
	ThinkingDuration       time.Duration     `json:"thinking_duration,omitempty"`
	ThinkingDisplay        reasoning.Display `json:"thinking_display,omitempty"`
	LifecyclePhase         string            `json:"lifecycle_phase,omitempty"`
	LifecycleProgress      int               `json:"lifecycle_progress,omitempty"`
	LifecycleProvider      string            `json:"lifecycle_provider,omitempty"`
	LifecycleUploadedBytes int64             `json:"lifecycle_uploaded_bytes,omitempty"`
	LifecycleTotalBytes    int64             `json:"lifecycle_total_bytes,omitempty"`
	LifecycleBeforeTokens  int               `json:"lifecycle_before_tokens,omitempty"`
	LifecycleAfterTokens   int               `json:"lifecycle_after_tokens,omitempty"`
	LifecycleOperationID   string            `json:"lifecycle_operation_id,omitempty"`
	LifecycleTrigger       string            `json:"lifecycle_trigger,omitempty"`
	LifecycleStageName     string            `json:"lifecycle_stage_name,omitempty"`
	LifecycleStageIndex    int               `json:"lifecycle_stage_index,omitempty"`
	LifecycleCompleted     int               `json:"lifecycle_completed,omitempty"`
	LifecycleStageCount    int               `json:"lifecycle_stage_count,omitempty"`
	LifecycleRetryAttempt  int               `json:"lifecycle_retry_attempt,omitempty"`
	LifecycleRetryAt       time.Time         `json:"lifecycle_retry_at,omitempty"`
	LifecycleRetryLimit    int               `json:"lifecycle_retry_limit,omitempty"`
	LifecycleContextWindow int               `json:"lifecycle_context_window,omitempty"`
	HistoryPathCount       int               `json:"history_path_count,omitempty"`
	HistoryTargetSnapshot  string            `json:"history_target_snapshot,omitempty"`
	HistorySafetySnapshot  string            `json:"history_safety_snapshot,omitempty"`
	Incomplete             bool              `json:"incomplete,omitempty"`
	IncompleteReason       string            `json:"incomplete_reason,omitempty"`
	SubPaneID              string            `json:"sub_pane_id,omitempty"`
	PermTag                string            `json:"perm_tag,omitempty"`
	PendingSteering        bool              `json:"pending_steering,omitempty"`
	Recovery               *recovery.Status  `json:"recovery,omitempty"`
}

// TurnIdentity is the immutable backend identity resolved when one turn
// starts. Settings affect later turns; they never rewrite this identity on
// messages already produced by an admitted turn.
type TurnIdentity struct {
	Generation uint64
	ProviderID string
	AccountID  string
	ModelID    string
}

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

// BlockOutcome identifies why a blocking tool request resumed. Keeping this
// cause separate from the response text prevents parser and lifecycle failures
// from being misreported to the model as a user cancellation.
type BlockOutcome string

const (
	BlockAnswered        BlockOutcome = "answered"
	BlockUserCancelled   BlockOutcome = "user_cancelled"
	BlockValidationError BlockOutcome = "validation_error"
)

// BlockResponse is correlated to one blocking tool call. RequestID is the
// agent-assigned internal tool ID, which prevents a late response from an old
// panel from resolving a newer request.
type BlockResponse struct {
	RequestID       string
	Outcome         BlockOutcome
	Value           string
	Detail          string
	AttributionKind string
	AttributionID   string
}

const (
	RoleUser          = "user"
	RoleAssistant     = "assistant"
	RoleThinking      = "thinking"
	RoleTool          = "tool"
	RoleError         = "error"
	RoleStatus        = "status"
	RoleAssignment    = "assignment"
	RoleSessionPicker = "sessionpicker"
)

const (
	ConfirmAllow  = "allow"
	ConfirmAlways = "always"
	ConfirmDeny   = "deny"
)

type CategorySettings struct {
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
	// "priority", "flex"); empty uses the account's setting. A tier the
	// connection's protocol does not offer is ignored.
	ServiceTier string `json:"service_tier,omitempty"`
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
	AgentProfiles map[string]AgentProfileSettings `json:"agent_profiles,omitempty"`
}

// AgentProfileSettings is a named, reusable specialization of a built-in role.
type AgentProfileSettings struct {
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
	ModelContextWindow       int               `json:"model_context_window,omitempty"`
	ModelMaxOutput           int               `json:"model_max_output,omitempty"`
	ModelModalities          []string          `json:"model_modalities"`
	ModelTemperature         *float64          `json:"model_temperature,omitempty"`
	ModelTopP                *float64          `json:"model_top_p,omitempty"`
}

// DefaultAgentRoleCategories is the single reset/initialization source for
// shipped roles. Memory inherits the main connection unless explicitly edited.
func DefaultAgentRoleCategories() map[string]CategorySettings {
	return map[string]CategorySettings{
		"default": {},
		"researcher": {
			Policy: "read-only", Description: "Gatherer - explores codebase, searches web, runs experiments",
			AllowedTools: []string{"read_file", "glob_files", "grep_files", "list_directory", "web_search", "web_fetch", "arxiv_search", "publish_report", "question"},
			DeniedTools:  []string{"write_file", "edit_file", "delete_file", "move_file", "copy_file", "execute_command", "async_execute_command"},
		},
		"crafter": {Policy: "no-delete", Description: "Implementer - writes code, edits files, runs tests", DeniedTools: []string{"delete_file"}},
		"aeron": {
			Policy: "read-only", Description: "Grunt - formatting, linting, boilerplate, monitoring",
			DeniedTools: []string{"delete_file", "move_file", "copy_file", "execute_command", "async_execute_command", "web_search", "web_fetch", "arxiv_search"},
		},
		"memory": {},
	}
}

func CloneAgentProfiles(source map[string]AgentProfileSettings) map[string]AgentProfileSettings {
	if source == nil {
		return nil
	}
	out := make(map[string]AgentProfileSettings, len(source))
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

type Settings struct {
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
	TurnLogsEnabled  string
	Agents           AgentsSettings
	Memory           MemorySettings              `json:"memory,omitempty"`
	Capabilities     captypes.Settings           `json:"capabilities,omitempty"`
	Safety           SafetySettings              `json:"safety,omitempty"`
	LocalHistory     LocalHistorySettings        `json:"local_history,omitempty"`
	SessionOverrides map[string]CategorySettings `json:"-"`
	// SessionMultiAgent is the explicit session override for sub-agent
	// spawning. nil means inherit the workspace/profile default.
	SessionMultiAgent *bool `json:"-"`
	// SessionCodeIntelligence lets this session's agents use language servers
	// independently of the editor; nil follows the workspace LSP switch.
	SessionCodeIntelligence *bool `json:"-"`
	// SessionMaxSubAgents caps this session's concurrently running sub-agents;
	// nil follows the workspace setting.
	SessionMaxSubAgents *int `json:"-"`
	// SessionDisabledCapabilities lists installed skill/MCP IDs this session
	// and every agent it runs have turned off. Sorted; nil means none.
	SessionDisabledCapabilities []string       `json:"-"`
	Theme                       ThemeSettings  `json:"theme,omitempty"`
	Editor                      EditorSettings `json:"editor,omitempty"`
	LSP                         LSPSettings    `json:"lsp,omitempty"`
	UI                          UISettings     `json:"ui,omitempty"`
}

type AppSettings struct {
	// Observation revisions fence a connected form's original application and
	// workspace preimages. They are client metadata, never persisted settings.
	ApplicationRevision string                      `json:"-"`
	WorkspaceRevision   string                      `json:"-"`
	Theme               ThemeSettings               `json:"theme,omitempty"`
	Editor              EditorSettings              `json:"editor,omitempty"`
	LSP                 LSPSettings                 `json:"lsp,omitempty"`
	UI                  UISettings                  `json:"ui,omitempty"`
	Accounts            map[string]provider.Account `json:"accounts,omitempty"`
	Agents              AgentsSettings              `json:"agents,omitempty"`
	Memory              MemorySettings              `json:"memory,omitempty"`
	Capabilities        captypes.Settings           `json:"capabilities,omitempty"`
	Safety              SafetySettings              `json:"safety,omitempty"`
	LocalHistory        LocalHistorySettings        `json:"local_history,omitempty"`
	Terminal            TerminalSettings            `json:"terminal,omitempty"`
	// TerminalShells are the shells installed on the core's machine. They are
	// observation metadata for the settings form, never persisted settings.
	TerminalShells []TerminalShell `json:"-"`
	// Network is application-owned: trust, proxy credentials and client
	// identities must never be promoted by a project's settings overlay.
	Network network.Settings `json:"network,omitempty"`
}

type CommandSeverityRule struct {
	Pattern string `json:"pattern"`
	Risk    string `json:"risk"`
}

type SafetySettings struct {
	ConfirmAt           string                 `json:"confirm_at,omitempty"`
	UnknownCommandRisk  string                 `json:"unknown_command_risk,omitempty"`
	ScriptExecutionRisk string                 `json:"script_execution_risk,omitempty"`
	CommandRules        []CommandSeverityRule  `json:"command_rules,omitempty"`
	NotifyDangerous     string                 `json:"notify_dangerous,omitempty"`
	AutoPermission      AutoPermissionSettings `json:"auto_permission,omitempty"`
}

// AutoPermissionSettings configures the permission reviewer used independently
// by each root and child. Provider and Model are deliberately explicit: an
// empty value means unconfigured rather than inheriting the acting model.
type AutoPermissionSettings struct {
	Enabled              string `json:"enabled,omitempty"`
	AccountID            string `json:"account_id,omitempty"`
	Provider             string `json:"provider,omitempty"`
	Model                string `json:"model,omitempty"`
	DenyMode             string `json:"deny_mode,omitempty"`
	UnresolvedMode       string `json:"unresolved_mode,omitempty"`
	Instructions         string `json:"instructions,omitempty"`
	ThinkingEffort       string `json:"thinking_effort,omitempty"`
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
func (s AutoPermissionSettings) Normalized() AutoPermissionSettings {
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
func (s AutoPermissionSettings) ReviewOutputTokens(attempt int) int {
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

type LocalHistorySettings struct {
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

func (s LocalHistorySettings) EnabledValue() bool         { return s.Enabled != "off" }
func (s LocalHistorySettings) UseGitIgnoreValue() bool    { return s.UseGitIgnore != "off" }
func (s LocalHistorySettings) CaptureExternalValue() bool { return s.CaptureExternalChanges != "off" }
func (s LocalHistorySettings) SafetySnapshotValue() bool {
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
	APIKey          string         `json:"-"`
	Model           string         `json:"model,omitempty"`
	ProviderID      string         `json:"provider_id,omitempty"`
	AccountID       string         `json:"account_id,omitempty"`
	ProviderURL     string         `json:"provider_url,omitempty"`
	ProviderAPIKey  string         `json:"-"`
	APIStandard     string         `json:"api_standard,omitempty"`
	ThinkingEffort  string         `json:"thinking_effort,omitempty"`
	TurnLogsEnabled string         `json:"turn_logs_enabled,omitempty"`
	Agents          AgentsSettings `json:"agents,omitempty"`
}

func (s Settings) AppPart() AppSettings {
	return AppSettings{
		Theme:        s.Theme,
		Editor:       s.Editor,
		LSP:          s.LSP,
		UI:           s.UI,
		LocalHistory: s.LocalHistory,
	}
}

func (s Settings) SessionPart() SessionSettings {
	return SessionSettings{
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

type ThemeSettings struct {
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
func (t ThemeSettings) PadTopOrDefault() int { return padWithDefault(t.PadTop, DefaultThemeFramePad) }

// PadBottomOrDefault returns the effective bottom frame padding.
func (t ThemeSettings) PadBottomOrDefault() int {
	return padWithDefault(t.PadBottom, DefaultThemeFramePad)
}

// PadLeftOrDefault returns the effective left frame padding.
func (t ThemeSettings) PadLeftOrDefault() int { return padWithDefault(t.PadLeft, DefaultThemeFramePad) }

// PadRightOrDefault returns the effective right frame padding.
func (t ThemeSettings) PadRightOrDefault() int {
	return padWithDefault(t.PadRight, DefaultThemeFramePad)
}

type AutoSaveMode string

const (
	AutoSaveOff               AutoSaveMode = "off"
	AutoSaveAfterDelay        AutoSaveMode = "after_delay"
	AutoSaveEditorFocusChange AutoSaveMode = "editor_focus_change"
	AutoSaveTerminalFocusLoss AutoSaveMode = "terminal_focus_loss"
)

type EditorSettings struct {
	TabSize          int          `json:"tab_size,omitempty"`
	WordWrap         bool         `json:"word_wrap,omitempty"`
	LineNumbers      bool         `json:"line_numbers,omitempty"`
	SyntaxTheme      string       `json:"syntax_theme,omitempty"`
	Minimap          bool         `json:"minimap,omitempty"`
	AutoSave         AutoSaveMode `json:"auto_save"`
	AutoSaveDelay    int          `json:"auto_save_delay,omitempty"`
	RenderWhitespace string       `json:"render_whitespace,omitempty"`
	FormatOnSave     bool         `json:"format_on_save,omitempty"`
}

// LSPLanguageSettings is the per-language overlay for one language server.
// Empty Command/Args inherit the built-in default for that language.
type LSPLanguageSettings struct {
	Enabled               bool              `json:"enabled"`
	Command               string            `json:"command,omitempty"`
	Args                  []string          `json:"args,omitempty"`
	Extensions            []string          `json:"extensions,omitempty"`
	LanguageID            string            `json:"language_id,omitempty"`
	RootMarkers           []string          `json:"root_markers,omitempty"`
	Environment           map[string]string `json:"environment,omitempty"`
	Settings              map[string]any    `json:"settings,omitempty"`
	InitializationOptions map[string]any    `json:"initialization_options,omitempty"`
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
	MinSeverity        string                         `json:"min_severity,omitempty"`
	UpdateDebounceMS   int                            `json:"update_debounce_ms,omitempty"`
	ServerThreads      int                            `json:"server_threads,omitempty"`
	ServerMemoryMB     int                            `json:"server_memory_mb,omitempty"`
	MaxServers         int                            `json:"max_servers,omitempty"`
	IdleTimeoutSeconds int                            `json:"idle_timeout_seconds,omitempty"`
	MaxDocumentBytes   int                            `json:"max_document_bytes,omitempty"`
	Languages          map[string]LSPLanguageSettings `json:"languages,omitempty"`
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
	// ContextHelp is compact, expanded, or off. Full Help remains available.
	ContextHelp string `json:"context_help,omitempty"`
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
}

// ModelCatalogUpdatedMsg is silent metadata invalidation, not a notification.
type ModelCatalogUpdatedMsg struct{ Generation uint64 }

// ModelEntriesFromInfos converts provider model infos into UI model entries,
// enriching each with the cached public-source metadata (pricing, context
// window, capabilities). Context window from the wire wins over the catalog.
func ModelEntriesFromInfos(models []provider.ModelInfo) []ModelEntry {
	entries := make([]ModelEntry, 0, len(models))
	for _, model := range models {
		entry := ModelEntry{
			ID: model.ID, Name: model.Name, ContextWindow: model.ContextWindow,
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
			entry.MaxOutput = meta.MaxOutput
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

// CompactResultMsg reports the outcome of a forced context compaction.
type CompactResultMsg struct {
	Desc        string
	Error       string
	Before      int
	After       int
	OperationID string
	Cancelled   bool
}

type ChatMessage struct {
	InternalID      string
	Role            string
	Origin          provider.MessageOrigin
	Content         string
	Collaboration   *provider.CollaborationAttribution
	Recovery        *recovery.Status
	ReferenceLabels []string
	Time            time.Time
	TurnGeneration  uint64
	ProviderID      string
	AccountID       string
	ModelID         string

	ToolName    string
	ToolArgs    string
	ToolCommand string
	ToolInput   string
	ToolScope   string
	ToolID      string
	ToolRoundID string
	ToolStatus  string
	ToolIndex   int

	ToolProgressPhase   string
	ToolProgressMessage string
	ToolProgressLabel   string
	ToolPreviewPhase    string
	ToolEstTokens       int
	ToolLivedAdded      int
	ToolLivedRemoved    int
	ToolStartTime       time.Time
	ToolDuration        time.Duration

	ThinkingActive         bool
	ThinkingInterrupted    bool
	ThinkingDuration       time.Duration
	ThinkingDisplay        reasoning.Display
	LifecyclePhase         string
	LifecycleProgress      int
	LifecycleProvider      string
	LifecycleUploadedBytes int64
	LifecycleTotalBytes    int64
	LifecycleBeforeTokens  int
	LifecycleAfterTokens   int
	LifecycleOperationID   string
	LifecycleTrigger       string
	LifecycleStageName     string
	LifecycleStageIndex    int
	LifecycleCompleted     int
	LifecycleStageCount    int
	LifecycleRetryAttempt  int
	LifecycleRetryAt       time.Time
	LifecycleRetryLimit    int
	LifecycleContextWindow int
	HistoryPathCount       int
	HistoryTargetSnapshot  string
	HistorySafetySnapshot  string

	Incomplete       bool
	IncompleteReason string

	SubPaneID string

	PermTag         string
	PendingSteering bool

	CachedLines          []string
	CursorLine           int
	StreamCompletedLines []string
	StreamCompletedLen   int
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
	AccountID  string
	BaseURL    string
}

type SaveSettingsMsg Settings
type UpdateModelMsg string
type ModelsListMsg []ModelEntry
type ModelsErrorMsg string

type SelectionTickMsg time.Time
type HistoryMessagesMsg []ChatMessage

// MaxConcurrentSubAgents bounds the configurable sub-agent limit. Every
// running child holds a model connection and tool processes.
const MaxConcurrentSubAgents = 32
