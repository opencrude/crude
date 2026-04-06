package statemachine

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
func Complete(response string, outputData map[string]any) *StepResult {
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
	return r
}

// InProgress creates a step result indicating asynchronous work is underway.
func InProgress(message string) *StepResult {
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
	return r
}
