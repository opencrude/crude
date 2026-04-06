package statemachine

import "time"

// BlockingConditionType enumerates the reasons a goal can be blocked.
type BlockingConditionType int

const (
	BlockAuthRequired      BlockingConditionType = iota
	BlockMissingParameter
	BlockConfirmationNeeded
	BlockRateLimitExceeded
	BlockExternalAPIError
	BlockAmbiguousInput
	BlockPendingSubGoal
)

// BlockingCondition represents why a goal is currently blocked.
type BlockingCondition struct {
	ID           string
	Type         BlockingConditionType
	Message      string
	Context      map[string]any
	SubGoalIntent string
	IsBlocking   bool
	CreatedAt    time.Time
}

func newBlocker(t BlockingConditionType, msg string) *BlockingCondition {
	return &BlockingCondition{
		ID:         newID(),
		Type:       t,
		Message:    msg,
		Context:    map[string]any{},
		IsBlocking: true,
		CreatedAt:  time.Now().UTC(),
	}
}

// AuthRequired creates an auth-required blocker.
func AuthRequired(message string) *BlockingCondition {
	if message == "" {
		message = "Authentication is required to view personal information"
	}
	b := newBlocker(BlockAuthRequired, message)
	b.SubGoalIntent = "Login"
	return b
}

// MissingParameter creates a missing-parameter blocker.
func MissingParameter(paramName, message string) *BlockingCondition {
	b := newBlocker(BlockMissingParameter, message)
	b.Context["parameterName"] = paramName
	return b
}

// ConfirmationNeeded creates a confirmation-needed blocker.
func ConfirmationNeeded(message string) *BlockingCondition {
	return newBlocker(BlockConfirmationNeeded, message)
}

// PendingSubGoal creates a blocker with an associated sub-goal intent.
func PendingSubGoal(subGoalIntent, message string) *BlockingCondition {
	b := newBlocker(BlockPendingSubGoal, message)
	b.SubGoalIntent = subGoalIntent
	return b
}

// RateLimitExceeded creates a rate-limit blocker.
func RateLimitExceeded(message string) *BlockingCondition {
	if message == "" {
		message = "Rate limit exceeded"
	}
	return newBlocker(BlockRateLimitExceeded, message)
}
