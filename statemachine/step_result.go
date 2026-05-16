package statemachine

import "crude/logger"

var stepResultLogger *logger.Logger

func SetStepResultLogger(lg *logger.Logger) {
	stepResultLogger = lg
}

func stepLogIf(fn func(lg *logger.Logger)) {
	if stepResultLogger != nil {
		fn(stepResultLogger)
	}
}

// StepStatus is the outcome state of a goal step execution.
type StepStatus int

const (
	StepComplete StepStatus = iota
	StepBlocked
	StepNeedsMoreInfo
	StepInProgress
	StepFailed
)

// StepResult is returned from goal step execution and drives the conversation flow.
type StepResult struct {
	Status               StepStatus
	Response             string
	OutcomeCode          string
	ErrorCode            string
	ActionType           string
	IsRetryable          bool
	DebugReason          string
	Blocker              *BlockingCondition
	SubGoals             []*Goal
	OutputData           map[string]any
	ShouldEndConversation bool
	RequiredSlots        []string
}

// Complete creates a successful step result.
func (s StepStatus) String() string {
	switch s {
	case StepComplete:
		return "complete"
	case StepBlocked:
		return "blocked"
	case StepNeedsMoreInfo:
		return "needs_more_info"
	case StepInProgress:
		return "in_progress"
	case StepFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func Complete(response string, outputData map[string]any) *StepResult {
	stepLogIf(func(lg *logger.Logger) { lg.Debug("STEP", "complete", "response_len", len(response), "output_keys", len(outputData)) })
	return &StepResult{
		Status:      StepComplete,
		Response:    response,
		OutcomeCode: OutcomeSuccess,
		ActionType:  ActionInform,
		OutputData:  outputData,
	}
}

// Blocked creates a blocked step result with an optional override response.
func Blocked(blocker *BlockingCondition, response string) *StepResult {
	if response == "" {
		response = blocker.Message
	}
	r := &StepResult{
		Status:      StepBlocked,
		Blocker:     blocker,
		Response:    response,
		OutcomeCode: OutcomeBlocked,
		ActionType:  ActionResolveBlocker,
		IsRetryable: true,
	}
	if blocker.SubGoalIntent != "" {
		r.SubGoals = []*Goal{
			NewGoal(blocker.SubGoalIntent, blocker.Message, PriorityNormal, blocker.IsBlocking),
		}
	}
	stepLogIf(func(lg *logger.Logger) { lg.Info("STEP", "blocked", "blocker_type", blocker.Type.String(), "sub_goal", blocker.SubGoalIntent) })
	return r
}

// NeedsMoreInfo creates a step result requesting additional slot values.
func NeedsMoreInfo(message string, requiredSlots []string) *StepResult {
	r := &StepResult{
		Status:        StepNeedsMoreInfo,
		Response:      message,
		OutcomeCode:   OutcomeNeedsInfo,
		ActionType:    ActionCollectSlots,
		IsRetryable:   true,
		RequiredSlots: requiredSlots,
	}
	if len(requiredSlots) > 0 {
		r.OutputData = map[string]any{"requiredSlots": requiredSlots}
	}
	stepLogIf(func(lg *logger.Logger) { lg.Info("STEP", "needs_more_info", "slots", requiredSlots) })
	return r
}

// InProgress creates a step result indicating asynchronous work is underway.
func InProgress(message string) *StepResult {
	stepLogIf(func(lg *logger.Logger) { lg.Debug("STEP", "in_progress", "message_len", len(message)) })
	return &StepResult{
		Status:      StepInProgress,
		Response:    message,
		OutcomeCode: OutcomeInProgress,
		ActionType:  ActionWait,
	}
}

// Failed creates a failed step result with optional debug details.
func Failed(message, errorDetails string) *StepResult {
	r := &StepResult{
		Status:      StepFailed,
		Response:    message,
		OutcomeCode: OutcomeFailed,
		ErrorCode:   ErrUnspecifiedFailure,
		ActionType:  ActionRetryOrEscalate,
		IsRetryable: true,
		DebugReason: errorDetails,
	}
	if errorDetails != "" {
		r.OutputData = map[string]any{"errorDetails": errorDetails}
	}
	stepLogIf(func(lg *logger.Logger) { lg.Error("STEP", "failed", "message", message, "details", errorDetails) })
	return r
}
