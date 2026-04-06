package dag

import "time"

// NodeStatus tracks the execution state of a DAG node.
type NodeStatus int

const (
	Pending    NodeStatus = iota // Not yet started; waiting on dependencies.
	Executing                    // Currently running.
	Successful                   // Completed without error.
	Err                          // Failed; includes error context.
	Blocking                     // Cannot proceed without external input.
	Skipped                      // Skipped due to upstream failure.
)

func (s NodeStatus) String() string {
	switch s {
	case Pending:
		return "pending"
	case Executing:
		return "executing"
	case Successful:
		return "successful"
	case Err:
		return "error"
	case Blocking:
		return "blocking"
	case Skipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// Terminal returns true if the status is a terminal state.
func (s NodeStatus) Terminal() bool {
	return s == Successful || s == Err || s == Skipped
}

// CRUDEOp is one of the five CRUDE primitives.
type CRUDEOp string

const (
	OpCreate  CRUDEOp = "Create"
	OpRead    CRUDEOp = "Read"
	OpUpdate  CRUDEOp = "Update"
	OpDelete  CRUDEOp = "Delete"
	OpExecute CRUDEOp = "Execute"
)

// IsDestructive returns true if the operation modifies or deletes state.
func (op CRUDEOp) IsDestructive() bool {
	switch op {
	case OpCreate, OpUpdate, OpDelete, OpExecute:
		return true
	default:
		return false
	}
}

// Node is a single unit of work in the DAG.
type Node struct {
	// Fields from LLM JSON output.
	ID            string         `json:"id"`
	Operation     CRUDEOp        `json:"operation"`
	Description   string         `json:"description"`
	Tool          string         `json:"tool"`
	Parameters    map[string]any `json:"parameters,omitempty"`
	DependsOn     []string       `json:"depends_on"`
	BlockQuestion string         `json:"block_question,omitempty"`

	// Runtime state (not serialized from LLM output).
	Status   NodeStatus `json:"-"`
	Result   string     `json:"-"`
	RunErr   error      `json:"-"`
	Started  time.Time  `json:"-"`
	Finished time.Time  `json:"-"`

	// RollbackData stores information needed to undo this operation.
	// For file writes: original content. For edits: original string. For deletes: file content.
	RollbackData map[string]any `json:"-"`

	// RequiresConfirmation is set by the safety layer for destructive ops.
	RequiresConfirmation bool   `json:"-"`
	ConfirmationReason   string `json:"-"`
}

// Duration returns how long the node took to execute, or zero if not finished.
func (n *Node) Duration() time.Duration {
	if n.Started.IsZero() || n.Finished.IsZero() {
		return 0
	}
	return n.Finished.Sub(n.Started)
}

// DAG is a directed acyclic graph of execution nodes.
type DAG struct {
	Nodes      map[string]*Node
	Dependents map[string][]string // reverse index: nodeID → nodes that depend on it
}

// AuditEntry records a single operation execution for the audit trail.
type AuditEntry struct {
	NodeID      string         `json:"node_id"`
	Operation   CRUDEOp        `json:"operation"`
	Tool        string         `json:"tool"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	StartTime   time.Time      `json:"start_time"`
	EndTime     time.Time      `json:"end_time"`
	Duration    string         `json:"duration"`
	Status      string         `json:"status"`
	Result      string         `json:"result,omitempty"`
	Error       string         `json:"error,omitempty"`
}

// Event is emitted by the Executor during execution.
type Event struct {
	// Type is one of: "node_start", "node_done", "node_error", "blocking", "complete".
	Type string
	Node *Node
	Data any
}
