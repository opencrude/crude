package types

import (
	"crude/types/activity"
	"crude/types/reasoning"
	"crude/types/recovery"
	"time"
)

// ResourceKind identifies the owner of status, activity, and notification data.
type ResourceKind string

const (
	ResourceApplication ResourceKind = "application"
	ResourceWorkspace   ResourceKind = "workspace"
	ResourceAgent       ResourceKind = "agent"
	ResourceEditor      ResourceKind = "editor"
	ResourceTerminal    ResourceKind = "terminal"
	ResourceLSP         ResourceKind = "lsp"
	ResourceGit         ResourceKind = "git"
	ResourceHistory     ResourceKind = "history"
	ResourceMemory      ResourceKind = "memory"
	ResourceSearch      ResourceKind = "search"
	ResourceFile        ResourceKind = "file"
)

// ResourceRef is a stable, presentation-neutral status target.
type ResourceRef struct {
	Kind        ResourceKind
	ID          string
	WorkspaceID string
	SessionID   string
	ParentID    string
}

type StatusSeverity uint8

const (
	SeverityInfo StatusSeverity = iota
	SeverityWarning
	SeverityError
	SeverityCritical
)

type ValueConfidence uint8

const (
	ConfidenceUnknown ValueConfidence = iota
	ConfidenceMeasured
	ConfidenceEstimated
	ConfidencePartial
	ConfidenceUpperBound
)

// UsageValue preserves presence and confidence. Value=0 with Known=true is a
// measured zero; Known=false is unavailable and must not be formatted as zero.
type UsageValue struct {
	Value      int64
	Known      bool
	Confidence ValueConfidence
}

type CostValue struct {
	USD        float64
	Known      bool
	Confidence ValueConfidence
	Source     string
}

type UsageScope string

const (
	UsageThisAgent UsageScope = "agent"
	UsageSubtree   UsageScope = "subtree"
	UsageSession   UsageScope = "session"
	UsageAuxiliary UsageScope = "auxiliary"
)

// UsageSummary is a non-overlapping rollup for one explicit scope.
type UsageSummary struct {
	Scope          UsageScope
	InputTotal     UsageValue
	UncachedInput  UsageValue
	CacheRead      UsageValue
	CacheWrite     UsageValue
	Output         UsageValue
	Reasoning      UsageValue
	ReasoningInOut bool
	ContextUsed    UsageValue
	ContextLimit   UsageValue
	Cost           CostValue
	Requests       int
	Turns          int
	TurnsKnown     bool
	AgentTurns     int
	Models         []ModelUsage
	ModelID        string
	ProviderID     string
	AccountID      string
	MeasuredAt     time.Time
}

// ModelUsage retains the producing account/provider/model identity, independent
// of the model selected for the next request. Turns count successful API calls;
// AgentTurns counts distinct actor generations and never substitutes for calls.
type ModelUsage struct {
	ModelID, ProviderID, AccountID string
	Turns, Requests                int
	AgentTurns                     int
	TurnsKnown                     bool
	Cost                           CostValue
}

type AgentActivityView struct {
	Summary                  string
	SuccessfulCalls          int
	CallsKnown               bool
	Active                   bool
	CompactionPhase          string
	Status                   AgentStatusSnapshot
	Thinking                 string
	ThinkingID               string
	ThinkingExpanded         bool
	ThinkingDisplay          reasoning.Display
	TideForward              bool
	RetryAttempt, RetryLimit int
	RetryReason              string
	Plan                     *activity.Plan
	Recovery                 recovery.Status
}

type AgentPhase string

const (
	AgentOpening          AgentPhase = "opening"
	AgentIdle             AgentPhase = "idle"
	AgentPreparing        AgentPhase = "preparing"
	AgentUploading        AgentPhase = "uploading"
	AgentWaiting          AgentPhase = "waiting_provider"
	AgentCompacting       AgentPhase = "compacting_context"
	AgentCompactionResult AgentPhase = "compaction_result"
	AgentThinking         AgentPhase = "thinking"
	AgentResponding       AgentPhase = "responding"
	AgentTool             AgentPhase = "tool"
	AgentChildren         AgentPhase = "children"
	AgentApproval         AgentPhase = "approval"
	AgentQuestion         AgentPhase = "question"
	AgentRetrying         AgentPhase = "retrying"
	AgentRecovering       AgentPhase = "recovering"
	AgentNeedsAttention   AgentPhase = "needs_attention"
	AgentInterrupted      AgentPhase = "interrupted"
	AgentCancelling       AgentPhase = "cancelling"
	AgentCompleted        AgentPhase = "completed"
	AgentCancelled        AgentPhase = "cancelled"
	AgentFailed           AgentPhase = "failed"
)

type AgentStatusSnapshot struct {
	Ref            ResourceRef
	Label          string
	Phase          AgentPhase
	Detail         string
	ProviderID     string
	AccountID      string
	ModelID        string
	TurnGeneration uint64
	RequestID      string
	Progress       int
	ProgressKnown  bool
	UploadedBytes  int64
	TotalBytes     int64
	StartedAt      time.Time
	PhaseStartedAt time.Time
	UpdatedAt      time.Time
	RetryAt        time.Time
	Hidden         bool
	ReadOnly       bool
	NeedsAction    bool
	Unsafe         bool
	ActiveTools    int
	ActiveChildren int
	// Checkpoint is background context preparation: "preparing", "ready" or
	// empty. It never blocks the agent and is not part of Phase.
	Checkpoint string
}

type OperationState string

const (
	OperationQueued     OperationState = "queued"
	OperationRunning    OperationState = "running"
	OperationWaiting    OperationState = "waiting"
	OperationCancelling OperationState = "cancelling"
	OperationSucceeded  OperationState = "succeeded"
	OperationFailed     OperationState = "failed"
	OperationCancelled  OperationState = "cancelled"
)

type StatusAction struct {
	ID      string
	Label   string
	Enabled bool
	Reason  string
}

type OperationSnapshot struct {
	ID          string
	Owner       ResourceRef
	Kind        string
	Label       string
	Detail      string
	State       OperationState
	Progress    int64
	Total       int64
	Unit        string
	StartedAt   time.Time
	UpdatedAt   time.Time
	EndedAt     time.Time
	Cancellable bool
	OutputRef   string
	Error       string
	Actions     []StatusAction
}

type NotificationRecord struct {
	ID          string
	DedupKey    string
	Owner       ResourceRef
	Severity    StatusSeverity
	Source      string
	Summary     string
	Detail      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ResolvedAt  time.Time
	Occurrences int
	Read        bool
	Dismissed   bool
	Transient   bool
	Actions     []StatusAction
}

type IssueSnapshot struct {
	ID         string
	Owner      ResourceRef
	Severity   StatusSeverity
	Summary    string
	Detail     string
	UpdatedAt  time.Time
	ResolvedAt time.Time
	Actions    []StatusAction
}

type LSPServerHealth struct {
	ID              string
	Name            string
	RootURI         string
	Status          string
	Error           string
	Connected       bool
	Starting        bool
	Syncing         bool
	Busy            bool
	FilesTracked    int
	FilesAffected   int
	Errors          int
	Warnings        int
	Information     int
	Hints           int
	LastDiagnostics time.Time
}
