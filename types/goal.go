package types

import "time"

// GoalStatus is where a session's /goal stands.
type GoalStatus string

const (
	// GoalActive: the agent keeps working toward the objective across turns.
	GoalActive GoalStatus = "active"
	// GoalPaused: the user stopped the work; /goal resume continues it.
	GoalPaused GoalStatus = "paused"
	// GoalBlocked: the work cannot progress without the user; Reason says why.
	GoalBlocked GoalStatus = "blocked"
	// GoalBudgetLimited: the goal used its token budget; no new work starts.
	GoalBudgetLimited GoalStatus = "budget_limited"
	// GoalComplete: an independent verifier confirmed the objective.
	GoalComplete GoalStatus = "complete"
)

// Goal is the detached view of a session's goal that every shell paints.
type Goal struct {
	ID        string     `json:"id"`
	Objective string     `json:"objective"`
	Status    GoalStatus `json:"status"`
	// Reason explains the latest status: what blocks or paused the goal, or
	// the verifier's findings.
	Reason     string `json:"reason,omitempty"`
	TokensUsed int64  `json:"tokens_used"`
	// TokenBudget caps TokensUsed when the user asked for one; 0 is none.
	TokenBudget int64 `json:"token_budget,omitempty"`
	// TimeUsed is the time spent with the goal active, across turns.
	TimeUsed time.Duration `json:"time_used"`
	// Verifying: an independent verifier is checking a completion claim.
	Verifying bool `json:"verifying,omitempty"`
	// Waiting: the goal is active but continues only once the user resumes
	// it or sends a message (after a cold reopen or a fork).
	Waiting   bool      `json:"waiting,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Unfinished reports whether a new objective would replace work in progress.
func (g Goal) Unfinished() bool { return g.Status != GoalComplete }
