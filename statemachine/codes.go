package statemachine

// Outcome codes returned by StepResult.
const (
	OutcomeSuccess    = "SUCCESS"
	OutcomeFailed     = "FAILED"
	OutcomeBlocked    = "BLOCKED"
	OutcomeNeedsInfo  = "NEEDS_MORE_INFO"
	OutcomeInProgress = "IN_PROGRESS"
)

// Action type hints for the consumer of a StepResult.
const (
	ActionInform             = "inform"
	ActionCollectSlots       = "collect_slots"
	ActionResolveGoalBlocker = "resolve_goal_blocker"
	ActionAuthenticate       = "authenticate"
	ActionReAuthenticate     = "re_authenticate"
	ActionRetryOrEscalate    = "retry_or_escalate"
	ActionWait               = "wait"
)

// Error codes for failed steps.
const (
	ErrUnspecifiedFailure = "UNSPECIFIED_FAILURE"
)
