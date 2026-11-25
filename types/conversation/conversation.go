package conversation

import (
	"time"

	"crude/agent/provider"
	"crude/types/attachment"
	"crude/types/reasoning"
	"crude/types/recovery"
	"crude/types/toolresult"
)

// Message is the presentation-neutral canonical message shared by
// live workspace state, durable session snapshots, and every frontend. Adding
// a lifecycle field here automatically includes it in JSON persistence; TUI
// rendering caches and selection state deliberately live elsewhere.
type Message struct {
	ExecutionID     string                             `json:"execution_id,omitempty"`
	InternalID      string                             `json:"internal_id,omitempty"`
	Role            string                             `json:"role"`
	Origin          provider.MessageOrigin             `json:"origin,omitempty"`
	Content         string                             `json:"content,omitempty"`
	ReferenceLabels []string                           `json:"reference_labels,omitempty"`
	Attachments     []attachment.Target                `json:"attachments,omitempty"`
	Time            time.Time                          `json:"time,omitempty"`
	TurnGeneration  uint64                             `json:"turn_generation,omitempty"`
	ProviderID      string                             `json:"provider_id,omitempty"`
	AccountID       string                             `json:"account_id,omitempty"`
	ModelID         string                             `json:"model_id,omitempty"`
	Collaboration   *provider.CollaborationAttribution `json:"collaboration,omitempty"`

	ToolName            string             `json:"tool_name,omitempty"`
	ToolArgs            string             `json:"tool_args,omitempty"`
	ToolCommand         string             `json:"tool_command,omitempty"`
	ToolInput           string             `json:"tool_input,omitempty"`
	ToolScope           string             `json:"tool_scope,omitempty"`
	ToolID              string             `json:"tool_id,omitempty"`
	ToolRoundID         string             `json:"tool_round_id,omitempty"`
	ToolStatus          string             `json:"tool_status,omitempty"`
	ToolOutcome         toolresult.Outcome `json:"tool_outcome,omitempty,omitzero"`
	ToolIndex           int                `json:"tool_index,omitempty"`
	ToolProgressPhase   string             `json:"tool_progress_phase,omitempty"`
	ToolProgressMessage string             `json:"tool_progress_message,omitempty"`
	ToolProgressLabel   string             `json:"tool_progress_label,omitempty"`
	ToolPreviewPhase    string             `json:"tool_preview_phase,omitempty"`
	ToolEstTokens       int                `json:"tool_est_tokens,omitempty"`
	ToolLinesAdded      int                `json:"tool_lines_added,omitempty"`
	ToolLinesRemoved    int                `json:"tool_lines_removed,omitempty"`
	ToolStartTime       time.Time          `json:"tool_start_time,omitempty"`
	ToolDuration        time.Duration      `json:"tool_duration,omitempty"`

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
	ExecutionID string
	Generation  uint64
	ProviderID  string
	AccountID   string
	ModelID     string
}

const (
	// ToolPreviewPreparing means the tool preview is still being prepared.
	ToolPreviewPreparing = "preparing"
	// ToolPreviewStreaming means the preview is streaming in.
	ToolPreviewStreaming = "streaming"
	// ToolPreviewDone means the preview is complete.
	ToolPreviewDone = "done"
	// ToolPreviewError means the preview failed.
	ToolPreviewError = "error"
)

const (
	// ToolStatusCalling means the tool call has started but has no result yet.
	ToolStatusCalling = "calling"
	// ToolStatusStreaming means the tool call is streaming its result.
	ToolStatusStreaming = "streaming"
	// ToolStatusSuccess means the tool call succeeded.
	ToolStatusSuccess = "success"
	// ToolStatusError means the tool call failed.
	ToolStatusError = "error"
	// ToolStatusCancelled means the tool call was cancelled.
	ToolStatusCancelled = "cancelled"
)

// BlockOutcome identifies why a blocking tool request resumed. Keeping this
// cause separate from the response text prevents parser and lifecycle failures
// from being misreported to the model as a user cancellation.
type BlockOutcome string

const (
	// BlockAnswered means the user answered the blocking request.
	BlockAnswered BlockOutcome = "answered"
	// BlockUserCancelled means the user cancelled the blocking request.
	BlockUserCancelled BlockOutcome = "user_cancelled"
	// BlockValidationError means the answer failed validation.
	BlockValidationError BlockOutcome = "validation_error"
)

// BlockResponse is correlated to one blocking tool call. RequestID is the
// agent-assigned internal tool ID, which prevents a late response from an old
// panel from resolving a newer request.
type BlockResponse struct {
	RequestID string
	Outcome   BlockOutcome
	Value     string
	// HumanAnswers is populated only by the human question resolver. It
	// preserves multiline values without parsing model-facing display text.
	HumanAnswers    [4]string
	Detail          string
	AttributionKind string
	AttributionID   string
}

const (
	// RoleUser marks a message that the user sent.
	RoleUser = "user"
	// RoleAssistant marks a message that the model wrote.
	RoleAssistant = "assistant"
	// RoleThinking marks a reasoning summary.
	RoleThinking = "thinking"
	// RoleTool marks a tool call or its result.
	RoleTool = "tool"
	// RoleError marks a failure shown in the transcript.
	RoleError = "error"
	// RoleStatus marks a runtime status line.
	RoleStatus = "status"
	// RoleAssignment marks a delegation assignment.
	RoleAssignment = "assignment"
	// RoleSessionPicker marks the session picker row.
	RoleSessionPicker = "sessionpicker"
)

const (
	// ConfirmAllow lets this one operation run.
	ConfirmAllow = "allow"
	// ConfirmAlways lets this kind of operation run from now on.
	ConfirmAlways = "always"
	// ConfirmDeny rejects the operation.
	ConfirmDeny = "deny"
)

// ConfirmResult carries the choice that the user made in a confirmation.
type ConfirmResult struct {
	Choice    string
	Operation string
	Tool      string
	Target    string
}

// QuestionResult carries the answers that the user gave to a question.
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

// ChatMessage is one transcript row in the shape that the shell renders.
type ChatMessage struct {
	ExecutionID     string
	InternalID      string
	Role            string
	Origin          provider.MessageOrigin
	Content         string
	Collaboration   *provider.CollaborationAttribution
	Recovery        *recovery.Status
	ReferenceLabels []string
	Attachments     []attachment.Target
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
	ToolOutcome toolresult.Outcome
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

	CachedLines []string
	CursorLine  int
}

// IsHumanSteering identifies an authored interruption, even when it carries
// collaboration provenance for the durable admission.
func (m *ChatMessage) IsHumanSteering() bool {
	return m.Role == RoleUser && m.Origin == provider.OriginHumanSteering
}
