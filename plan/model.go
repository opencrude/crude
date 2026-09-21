// Package plan owns the presentation-neutral planning domain for a session.
//
// A plan is a revisioned, declarative snapshot that models can create, edit,
// reorder, skip, reopen, pause, resume, and cancel in one atomic update. Both
// the main agent and sub-agents share the same Service implementation. The
// package must never import crude/tui; the TUI consumes a neutral PlanView.
package plan

import (
	"context"
	"fmt"
	"time"
)

// PlanStatus is the lifecycle state of a plan.
type PlanStatus string

const (
	StatusActive    PlanStatus = "active"
	StatusPaused    PlanStatus = "paused"
	StatusCompleted PlanStatus = "completed"
	StatusCancelled PlanStatus = "cancelled"
)

// Valid reports whether s is a known plan status.
func (s PlanStatus) Valid() bool {
	switch s {
	case StatusActive, StatusPaused, StatusCompleted, StatusCancelled:
		return true
	}
	return false
}

// StepStatus is the lifecycle state of one plan step.
type StepStatus string

const (
	StepPending    StepStatus = "pending"
	StepInProgress StepStatus = "in_progress"
	StepCompleted  StepStatus = "completed"
	StepSkipped    StepStatus = "skipped"
	StepBlocked    StepStatus = "blocked"
	StepFailed     StepStatus = "failed"
	StepCancelled  StepStatus = "cancelled"
)

// Valid reports whether s is a known step status.
func (s StepStatus) Valid() bool {
	switch s {
	case StepPending, StepInProgress, StepCompleted, StepSkipped,
		StepBlocked, StepFailed, StepCancelled:
		return true
	}
	return false
}

// Step statuses are declarative lifecycle labels. The plan store does not
// interpret them as a scheduler or gate transitions using dependency order.

// Plan is the canonical, persisted plan snapshot.
type Plan struct {
	ID          string     `json:"id"`
	Revision    uint64     `json:"revision"`
	Objective   string     `json:"objective"`
	Status      PlanStatus `json:"status"`
	Explanation string     `json:"explanation,omitempty"`
	Steps       []PlanStep `json:"steps"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
}

// PlanStep is one ordered step within a plan.
type PlanStep struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Status      StepStatus `json:"status"`
	DependsOn   []string   `json:"depends_on,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	Attempts    int        `json:"attempts,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// UpdateRequest is the complete desired plan snapshot supplied by the model.
// It is declarative: the model provides the whole plan, not a delta operation.
type UpdateRequest struct {
	PlanID      string
	Revision    uint64
	Objective   string
	Status      PlanStatus
	Explanation string
	Steps       []PlanStep
}

// ChangeSet summarizes what an Update committed so the caller can report
// revision progress without echoing the full plan.
type ChangeSet struct {
	PlanID   string `json:"plan_id"`
	Revision uint64 `json:"revision"`
}

// PlanSummary is the compact list projection used by List.
type PlanSummary struct {
	ID        string     `json:"id"`
	Objective string     `json:"objective"`
	Status    PlanStatus `json:"status"`
	Revision  uint64     `json:"revision"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// PlanEvent is one durable revision event. Payload is the canonical plan
// snapshot committed at that revision.
type PlanEvent struct {
	Revision  uint64    `json:"revision"`
	Type      string    `json:"type"`
	Payload   Plan      `json:"plan"`
	CreatedAt time.Time `json:"created_at"`
}

// RevisionConflictError carries the canonical plan for a stale update so the
// tool adapter can return a structured conflict without string matching.
type RevisionConflictError struct {
	Current   Plan
	Requested uint64
}

func (e *RevisionConflictError) Error() string {
	return fmt.Sprintf("plan revision conflict: current revision is %d, request is %d", e.Current.Revision, e.Requested)
}

// Service is the contract both orchestrators and sub-agents use. It is
// deliberately session-scoped: a Store is constructed per session DB, so the
// interface carries no session-ID parameter. Active and List take a sessionID
// to satisfy callers that only have the session key and to keep the table's
// session_id column queryable.
type Service interface {
	Update(context.Context, UpdateRequest) (Plan, ChangeSet, error)
	Get(context.Context, string) (Plan, error)
	Active(context.Context, string) (*Plan, error)
	List(context.Context, string) ([]PlanSummary, error)
}
