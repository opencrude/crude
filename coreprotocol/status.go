package coreprotocol

import "crude/types"

// Status center methods. The status center is workspace-scoped shared state;
// shells keep their transient notices locally and replicate the rest.
const (
	StatusSnapshotMethod = "status.snapshot"
	StatusMutateMethod   = "status.mutate"
)

// StatusSnapshot is the complete replicated status center.
type StatusSnapshot struct {
	Version       uint64                      `json:"version"`
	Agents        []types.AgentStatusSnapshot `json:"agents"`
	Issues        []types.IssueSnapshot       `json:"issues"`
	Notifications []types.NotificationRecord  `json:"notifications"`
}

// Status mutation operations.
const (
	StatusNotify          = "notify"
	StatusMarkRead        = "mark_read"
	StatusDismiss         = "dismiss"
	StatusSetIssue        = "set_issue"
	StatusResolveIssue    = "resolve_issue"
	StatusSetOperation    = "set_operation"
	StatusFinishOperation = "finish_operation"
)

// StatusMutation is one shell-originated status change.
type StatusMutation struct {
	Op           string                    `json:"op"`
	ID           string                    `json:"id,omitempty"`
	Read         bool                      `json:"read,omitempty"`
	Notification *types.NotificationRecord `json:"notification,omitempty"`
	Issue        *types.IssueSnapshot      `json:"issue,omitempty"`
	Operation    *types.OperationSnapshot  `json:"operation,omitempty"`
	State        types.OperationState      `json:"state,omitempty"`
	Error        string                    `json:"error,omitempty"`
}

// StatusMutationResult reports the identity the core assigned or reused.
type StatusMutationResult struct {
	ID      string `json:"id,omitempty"`
	Applied bool   `json:"applied"`
}
