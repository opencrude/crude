// Package control defines bounded, presentation-neutral control values. It does
// not offer commands, dispatch actions, authenticate callers, or grant authority.
// Owners must resolve actual effects, authorize them, and recheck fences at effect
// time. No Stage 1 control transport or UI implementation is implied.
package control

import "encoding/json"

const (
	MaxPageLimit = 128
	// MaxIDBytes matches the core wire metadata string ceiling.
	MaxIDBytes = 65536
	// Schemas and individual JSON values use a smaller discovery budget than
	// the core's 8 MiB frame ceiling. Aggregate transport budgets remain separate.
	MaxSchemaBytes = 64 << 10
	MaxValueBytes  = 64 << 10
)

type Scope string

const (
	Domain Scope = "domain"
	Shell  Scope = "shell"
)

// Effect has no default. A generic unresolved action must not claim Read;
// descriptor resolution must supply its actual effect before validation.
type Effect string

const (
	Read          Effect = "read"
	Write         Effect = "write"
	Delete        Effect = "delete"
	Execute       Effect = "execute"
	Communication Effect = "communication"
	Authority     Effect = "authority"
)

type ShellOwner struct {
	ClientID string `json:"client_id"`
	Epoch    uint64 `json:"epoch"`
}

type Target struct {
	WorkspaceID string      `json:"workspace_id"`
	ExecutionID string      `json:"execution_id,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
	ResourceID  string      `json:"resource_id,omitempty"`
	Shell       *ShellOwner `json:"shell,omitempty"`
}

type CallerKind string

const (
	AgentCaller    CallerKind = "agent"
	ExternalCaller CallerKind = "external_client"
)

// Caller is attribution only, never credential material or proof of a grant.
type Caller struct {
	Kind     CallerKind `json:"kind"`
	AgentID  string     `json:"agent_id,omitempty"`
	RunID    string     `json:"run_id,omitempty"`
	ClientID string     `json:"client_id,omitempty"`
}

// Schema carries JSON Schema metadata, not a validator implementation.
type Schema struct {
	ID       string          `json:"id"`
	Revision uint64          `json:"revision"`
	Document json.RawMessage `json:"document"`
}

type Requirements struct {
	Execution bool `json:"execution,omitempty"`
	Session   bool `json:"session,omitempty"`
	Resource  bool `json:"resource,omitempty"`
	Revision  bool `json:"revision,omitempty"`
	OpenEpoch bool `json:"open_epoch,omitempty"`
}

type Reason string

const (
	Unsupported        Reason = "unsupported"
	TargetUnavailable  Reason = "target_unavailable"
	HumanRequired      Reason = "human_required"
	PolicyDenied       Reason = "policy_denied"
	PreconditionFailed Reason = "precondition_failed"
)

type Availability struct {
	Available bool   `json:"available"`
	Reason    Reason `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
}

type Descriptor struct {
	ID           string       `json:"id"`
	Scope        Scope        `json:"scope"`
	Effect       Effect       `json:"effect"`
	HumanOnly    bool         `json:"human_only"`
	Input        Schema       `json:"input"`
	Output       Schema       `json:"output"`
	Requires     Requirements `json:"requires"`
	Availability Availability `json:"availability"`
}

// Revision is optional, allowing an explicit zero revision to be fenced.
type Preconditions struct {
	Revision  *uint64 `json:"revision,omitempty"`
	OpenEpoch *uint64 `json:"open_epoch,omitempty"`
}

type Request struct {
	ID            string          `json:"id"`
	CommandID     string          `json:"command_id"`
	Caller        Caller          `json:"caller"`
	Target        Target          `json:"target"`
	Preconditions Preconditions   `json:"preconditions"`
	Input         json.RawMessage `json:"input"`
}

// Current is an owner-resolved observation, not caller-supplied authority.
// All supplied target identities must match; nil fences mean unavailable.
type Current struct {
	Target        Target
	Preconditions Preconditions
}

type ResultState string

const (
	Admitted        ResultState = "admitted"
	Committed       ResultState = "committed"
	Applied         ResultState = "applied"
	PendingDecision ResultState = "pending_decision"
	Partial         ResultState = "partial"
	Failed          ResultState = "failed"
	Uncertain       ResultState = "uncertain"
)

type FaultCode string

const (
	Invalid         FaultCode = "invalid"
	Unavailable     FaultCode = "unavailable"
	Stale           FaultCode = "stale"
	Denied          FaultCode = "denied"
	OperationFailed FaultCode = "operation_failed"
	UnknownOutcome  FaultCode = "unknown_outcome"
)

type Fault struct {
	Code    FaultCode `json:"code"`
	Field   string    `json:"field,omitempty"`
	Message string    `json:"message"`
}

func (f *Fault) Error() string { return string(f.Code) + ": " + f.Field + ": " + f.Message }

// Result retains identities even on partial/uncertain outcomes. Applied means
// the owner applied an action, not that a physical terminal displayed a frame.
type Result struct {
	RequestID     string          `json:"request_id"`
	OperationID   string          `json:"operation_id"`
	State         ResultState     `json:"state"`
	ResourceIDs   []string        `json:"resource_ids,omitempty"`
	Revision      *uint64         `json:"revision,omitempty"`
	FrameRevision *uint64         `json:"frame_revision,omitempty"`
	Output        json.RawMessage `json:"output,omitempty"`
	Fault         *Fault          `json:"fault,omitempty"`
}

type Role string

const (
	Container Role = "container"
	Button    Role = "button"
	Field     Role = "field"
	List      Role = "list"
	Tree      Role = "tree"
	Item      Role = "item"
	Tab       Role = "tab"
	Window    Role = "window"
	Text      Role = "text"
	Scrollbar Role = "scrollbar"
	Divider   Role = "divider"
)

// Bounds use terminal cells, not bytes, runes, pixels, or character offsets.
type Bounds struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type NodeState struct {
	Enabled  bool `json:"enabled"`
	Visible  bool `json:"visible"`
	Focused  bool `json:"focused"`
	Selected bool `json:"selected"`
	Expanded bool `json:"expanded"`
}

// Node is a flat inspection record. Value must be nonsecret, owner-redacted
// evidence; validation cannot detect secrets or authorize private draft access.
// Actions refer to resolved descriptors, never generic keys or method pointers.
type Node struct {
	ID        string          `json:"id"`
	ParentID  string          `json:"parent_id,omitempty"`
	Role      Role            `json:"role"`
	Name      string          `json:"name"`
	Value     json.RawMessage `json:"value,omitempty"`
	Target    Target          `json:"target"`
	Revision  uint64          `json:"revision"`
	OpenEpoch uint64          `json:"open_epoch"`
	State     NodeState       `json:"state"`
	Actions   []Descriptor    `json:"actions,omitempty"`
	Bounds    *Bounds         `json:"bounds,omitempty"`
}

// Cursors are opaque owner-issued values, fenced by the owner's inventory
// revision. There is no unbounded/default page size or automatic cursor retry.
type PageRequest struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit"`
}

type Page struct {
	Nodes      []Node `json:"nodes"`
	NextCursor string `json:"next_cursor,omitempty"`
}
