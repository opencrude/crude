package collaboration

import (
	"crude/types/command"
	"errors"
	"time"
)

const (
	SchemaVersion = 1

	MaxBodyBytes       = 64 << 10
	MaxArtifactBytes   = 4 << 20
	MaxPageBytes       = 256 << 10
	MaxReferences      = 64
	DefaultPageLimit   = 40
	MaxPageLimit       = 200
	DefaultAgentLimit  = 40
	MaxAgentLimit      = 200
	DefaultReportLimit = 20
	MaxReportLimit     = 100
)

var (
	ErrNotFound         = errors.New("collaboration: not found")
	ErrConflict         = errors.New("collaboration: conflict")
	ErrInvalid          = errors.New("collaboration: invalid request")
	ErrTooLarge         = errors.New("collaboration: payload too large")
	ErrWorkspace        = errors.New("collaboration: workspace mismatch")
	ErrChangedRetry     = errors.New("collaboration: retry arguments changed")
	ErrStale            = errors.New("collaboration: stale version or runtime epoch")
	ErrUnavailable      = errors.New("collaboration: target unavailable")
	ErrAlreadyAdmitting = errors.New("collaboration: admission already in progress")
)

type AgentState string

const (
	AgentAvailable AgentState = "available"
	AgentSuspended AgentState = "suspended"
	AgentClosed    AgentState = "closed"
)

type RuntimePresence string

const (
	RuntimeAttached     RuntimePresence = "attached"
	RuntimeOffline      RuntimePresence = "offline"
	RuntimeUnresponsive RuntimePresence = "unresponsive"
)

type RunState string

const (
	RunQueued       RunState = "queued"
	RunRunning      RunState = "running"
	RunWaitingHuman RunState = "waiting_human"
	RunWaitingEvent RunState = "waiting_event"
	RunPausing      RunState = "pausing"
	RunPaused       RunState = "paused"
	RunStopping     RunState = "stopping"
	RunSucceeded    RunState = "succeeded"
	RunFailed       RunState = "failed"
	RunCancelled    RunState = "cancelled"
	RunInterrupted  RunState = "interrupted"
)

func (state RunState) Terminal() bool {
	switch state {
	case RunSucceeded, RunFailed, RunCancelled, RunInterrupted:
		return true
	default:
		return false
	}
}

type DeliveryState string

const (
	DeliveryPending    DeliveryState = "pending"
	DeliveryAdmitting  DeliveryState = "admitting"
	DeliveryAdmitted   DeliveryState = "admitted"
	DeliverySuperseded DeliveryState = "superseded"
	DeliveryWithdrawn  DeliveryState = "withdrawn"
	DeliveryRejected   DeliveryState = "rejected"
	DeliveryExpired    DeliveryState = "expired"
)

type RequestDisposition string

const (
	DispositionPending    RequestDisposition = "pending"
	DispositionAccepted   RequestDisposition = "accepted"
	DispositionDeferred   RequestDisposition = "deferred"
	DispositionDeclined   RequestDisposition = "declined"
	DispositionSuperseded RequestDisposition = "superseded"
)

type Origin string

const (
	OriginHuman   Origin = "human"
	OriginPeer    Origin = "peer"
	OriginRuntime Origin = "runtime"
)

type EventKind string

const (
	EventHumanMessage       EventKind = "human_message"
	EventHumanSteering      EventKind = "human_steering"
	EventPeerNote           EventKind = "peer_note"
	EventPeerReply          EventKind = "peer_reply"
	EventWorkRequest        EventKind = "work_request"
	EventWorkRevision       EventKind = "work_revision"
	EventRequestDisposition EventKind = "request_disposition"
	EventReportPublished    EventKind = "report_published"
	EventRunOutcome         EventKind = "run_outcome"
	EventPermissionRequest  EventKind = "permission_request"
	EventQuestionRequest    EventKind = "question_request"
	EventLifecycleControl   EventKind = "lifecycle_control"
	EventControlOutcome     EventKind = "control_outcome"
	EventCommandOutcome     EventKind = "command_outcome"
)

type ReportKind string

const (
	ReportInterim ReportKind = "interim"
	ReportFinal   ReportKind = "final"
	ReportAnswer  ReportKind = "answer"
)

type Reference struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision int    `json:"revision,omitempty"`
}

type Agent struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	SessionID        string          `json:"session_id"`
	ParentID         string          `json:"parent_id,omitempty"`
	Category         string          `json:"category,omitempty"`
	State            AgentState      `json:"state"`
	Presence         RuntimePresence `json:"presence"`
	RuntimeEpoch     uint64          `json:"runtime_epoch"`
	OwnerInstance    string          `json:"owner_instance,omitempty"`
	CurrentRunID     string          `json:"current_run_id,omitempty"`
	CurrentRequestID string          `json:"current_request_id,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	HeartbeatAt      time.Time       `json:"heartbeat_at,omitempty"`
}

type AgentRegistration struct {
	ID            string
	Name          string
	SessionID     string
	ParentID      string
	Category      string
	State         AgentState
	OwnerInstance string
	// Admissions are immutable local context-commit receipts recovered by the
	// execution owner before attaching. They are never supplied by a model.
	Admissions      []AdmissionReceipt
	PendingTerminal *FinishRunRequest
}

type AdmissionReceipt struct {
	DeliveryID      string
	EventID         string
	RuntimeEpoch    uint64
	DeliveryVersion uint64
	ContextCommitID string
}

type AgentQuery struct {
	Query         string
	SessionID     string
	ParentID      string
	State         AgentState
	Presence      RuntimePresence
	Limit         int
	IncludeClosed bool
}

type Run struct {
	ID              string    `json:"id"`
	AgentID         string    `json:"agent_id"`
	RuntimeEpoch    uint64    `json:"runtime_epoch"`
	RequestID       string    `json:"request_id,omitempty"`
	Origin          Origin    `json:"origin"`
	State           RunState  `json:"state"`
	Version         uint64    `json:"version"`
	Outcome         string    `json:"outcome,omitempty"`
	ContextCommitID string    `json:"context_commit_id,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	EndedAt         time.Time `json:"ended_at,omitempty"`
}

type StartRunRequest struct {
	ID           string
	AgentID      string
	RuntimeEpoch uint64
	RequestID    string
	Origin       Origin
}

type FinishRunRequest struct {
	RunID           string
	AgentID         string
	RuntimeEpoch    uint64
	ExpectedVersion uint64
	State           RunState
	Outcome         string
	ContextCommitID string
	SettlementEpoch uint64
}

type Recipient struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	State    AgentState      `json:"state"`
	Presence RuntimePresence `json:"presence"`
}

type PublishRequest struct {
	SenderID        string
	SenderName      string
	IdempotencyKey  string
	RecipientIDs    []string
	Broadcast       bool
	Origin          Origin
	Kind            EventKind
	Body            string
	ReplyTo         string
	RequestID       string
	RequestRevision uint64
	RunID           string
	Supersedes      string
	References      []Reference
	Command         *command.Outcome
	Execution       *ExecutionOutcome
	// AuditOnly is reserved for runtime outcomes addressed to the human UI;
	// it journals an event without pretending that "human" is a model runtime.
	AuditOnly bool
}

type Event struct {
	ID              string            `json:"id"`
	Sequence        int64             `json:"sequence"`
	WorkspaceID     string            `json:"workspace_id"`
	SenderID        string            `json:"sender_id"`
	SenderName      string            `json:"sender_name"`
	Recipients      []Recipient       `json:"recipients"`
	Broadcast       bool              `json:"broadcast,omitempty"`
	Origin          Origin            `json:"origin"`
	Kind            EventKind         `json:"kind"`
	Body            string            `json:"body"`
	ReplyTo         string            `json:"reply_to,omitempty"`
	ThreadID        string            `json:"thread_id"`
	RequestID       string            `json:"request_id,omitempty"`
	RequestRevision uint64            `json:"request_revision,omitempty"`
	RunID           string            `json:"run_id,omitempty"`
	Supersedes      string            `json:"supersedes,omitempty"`
	References      []Reference       `json:"references,omitempty"`
	Command         *command.Outcome  `json:"command,omitempty"`
	Execution       *ExecutionOutcome `json:"execution,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	Archived        bool              `json:"archived"`
}

// ExecutionOutcome is compact post-child disposition evidence, never policy or
// a rendered status string. Shared execution may have an empty ID.
type ExecutionOutcome struct {
	ID   string `json:"id,omitempty"`
	Root string `json:"root"`
	Mode string `json:"mode"`
}

type Delivery struct {
	ID              string        `json:"id"`
	EventID         string        `json:"event_id"`
	RecipientID     string        `json:"recipient_id"`
	State           DeliveryState `json:"state"`
	Version         uint64        `json:"version"`
	ReservedEpoch   uint64        `json:"reserved_epoch,omitempty"`
	ContextCommitID string        `json:"context_commit_id,omitempty"`
	Reason          string        `json:"reason,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
	AdmittedAt      time.Time     `json:"admitted_at,omitempty"`
}

type PublishResult struct {
	Event      Event      `json:"event"`
	Deliveries []Delivery `json:"deliveries"`
	Duplicate  bool       `json:"duplicate"`
}

type PendingDelivery struct {
	Event    Event    `json:"event"`
	Delivery Delivery `json:"delivery"`
}

type ReserveRequest struct {
	RecipientID  string
	RuntimeEpoch uint64
	Limit        int
	OnlyKinds    []EventKind
	ExcludeKinds []EventKind
	// ThroughSequence bounds passive startup admission to already-published
	// evidence. Zero means the current unbounded pending tail.
	ThroughSequence int64
}

type AdmitRequest struct {
	DeliveryID      string
	RecipientID     string
	RuntimeEpoch    uint64
	ExpectedVersion uint64
	ContextCommitID string
}

type ReleaseRequest struct {
	DeliveryID      string
	RecipientID     string
	RuntimeEpoch    uint64
	ExpectedVersion uint64
	Reason          string
	Reject          bool
}

type WithdrawRequest struct {
	EventID        string
	SenderID       string
	IdempotencyKey string
	Reason         string
}

type PageQuery struct {
	Since       int64
	Snapshot    int64
	ThreadID    string
	Search      string
	SenderID    string
	RecipientID string
	Kind        EventKind
	Limit       int
}

type Page struct {
	Events           []Event `json:"events"`
	Cursor           int64   `json:"cursor"`
	Snapshot         int64   `json:"snapshot"`
	LatestSequence   int64   `json:"latest_sequence"`
	HumanReadThrough int64   `json:"human_read_through"`
	HasMore          bool    `json:"has_more"`
	Bytes            int     `json:"bytes"`
}

type Artifact struct {
	ID                 string         `json:"id"`
	Revision           int            `json:"revision"`
	WorkspaceID        string         `json:"workspace_id"`
	AuthorID           string         `json:"author_id"`
	RunID              string         `json:"run_id,omitempty"`
	RequestID          string         `json:"request_id,omitempty"`
	Kind               ReportKind     `json:"kind"`
	Summary            string         `json:"summary"`
	Body               string         `json:"body"`
	Digest             string         `json:"digest"`
	SupersedesRevision int            `json:"supersedes_revision,omitempty"`
	References         []Reference    `json:"references,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
}

type PublishReportRequest struct {
	ArtifactID         string
	AuthorID           string
	IdempotencyKey     string
	RecipientIDs       []string
	RunID              string
	RequestID          string
	Kind               ReportKind
	Summary            string
	Body               string
	SupersedesRevision int
	References         []Reference
	Metadata           map[string]any
}

type PublishReportResult struct {
	Artifact Artifact      `json:"artifact"`
	Publish  PublishResult `json:"publish"`
}

type Claim struct {
	ID         string    `json:"id"`
	Path       string    `json:"path"`
	AgentID    string    `json:"agent_id"`
	RunID      string    `json:"run_id,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
	Version    uint64    `json:"version"`
	AcquiredAt time.Time `json:"acquired_at"`
	ReleasedAt time.Time `json:"released_at,omitempty"`
}

type AcquireClaimsRequest struct {
	AgentID   string
	RunID     string
	RequestID string
	Paths     []string
	// DelegatorID is the agent delegating this work (a launching parent). Its
	// own active claims that lie inside a requested path transfer to AgentID in
	// the same transaction; a delegator claim broader than the request still
	// conflicts, because releasing it would also release work not delegated.
	DelegatorID string
}

// AcquireClaimsResult reports the claims now held and any delegator claims
// that were released to make the hand-over.
type AcquireClaimsResult struct {
	Claims      []Claim
	Transferred []Claim
}

type ReleaseClaimsRequest struct {
	AgentID          string
	RunID            string
	ClaimIDs         []string
	ExpectedVersions map[string]uint64
}

type UpdateClaimsRequest struct {
	ParentID           string
	AgentID            string
	RuntimeEpoch       uint64
	RunID              string
	ExpectedRunVersion uint64
	ExpectedVersions   map[string]uint64
	Paths              []string
}

type UpdateClaimsResult struct {
	Released []Claim `json:"released"`
	Claims   []Claim `json:"claims"`
	Audit    Event   `json:"audit"`
}

type NarrowActiveClaimsRequest struct {
	AgentID            string
	RuntimeEpoch       uint64
	RunID              string
	ExpectedRunVersion uint64
	ExpectedVersions   map[string]uint64
	Paths              []string
}

type NarrowActiveClaimsResult struct {
	Released []Claim `json:"released"`
	Claims   []Claim `json:"claims"`
}
