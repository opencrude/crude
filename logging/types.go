package logging

import "time"

type LogLevel string

const (
	LevelDebug LogLevel = "DEBUG"
	LevelInfo  LogLevel = "INFO"
	LevelWarn  LogLevel = "WARN"
	LevelError LogLevel = "ERROR"
)

type LogCategory string

const (
	CategorySession LogCategory = "session"
	CategoryPhase   LogCategory = "phase"
	CategoryTool    LogCategory = "tool"
	CategoryLLM     LogCategory = "llm"
	CategoryDAG     LogCategory = "dag"
	CategoryTUI     LogCategory = "tui"
	CategoryState   LogCategory = "state"
	CategorySafety  LogCategory = "safety"
)

type LogEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     LogLevel       `json:"level"`
	Category  LogCategory    `json:"category"`
	Event     string         `json:"event"`
	Data      map[string]any `json:"data"`
	Duration  *int64         `json:"duration_ms,omitempty"`
}

const (
	EventSessionStart = "start"
	EventSessionEnd   = "end"
	EventSessionExit  = "exit"

	EventPhaseGroundingStart = "grounding_start"
	EventPhaseGroundingEnd   = "grounding_end"
	EventPhasePlanningStart  = "planning_start"
	EventPhasePlanningEnd    = "planning_end"
	EventPhaseExecutionStart = "execution_start"
	EventPhaseExecutionEnd   = "execution_end"
	EventPhaseSummarizeStart = "summarize_start"
	EventPhaseSummarizeEnd   = "summarize_end"
	EventPhaseChitchatStart  = "chitchat_start"
	EventPhaseChitchatEnd    = "chitchat_end"

	EventToolCall   = "call"
	EventToolResult = "result"
	EventToolError  = "error"

	EventLLMRequest     = "request"
	EventLLMResponse    = "response"
	EventLLMStreamStart = "stream_start"
	EventLLMStreamToken = "stream_token"
	EventLLMStreamEnd   = "stream_end"
	EventLLMError       = "error"

	EventDAGNodeStart    = "node_start"
	EventDAGNodeComplete = "node_complete"
	EventDAGNodeError    = "node_error"
	EventDAGNodeSkipped  = "node_skipped"
	EventDAGBlocking     = "blocking"
	EventDAGResume       = "resume"

	EventTUIEventSent = "event_sent"
	EventTUIUserInput = "user_input"

	EventStateGoalCreated  = "goal_created"
	EventStateGoalComplete = "goal_complete"
	EventStateGoalFailed   = "goal_failed"
	EventStateBlocked      = "blocked"

	EventSafetyConfirmationRequested = "confirmation_requested"
	EventSafetyConfirmationGranted   = "confirmation_granted"
	EventSafetyConfirmationDenied    = "confirmation_denied"
	EventSafetyRollbackTriggered     = "rollback_triggered"
	EventSafetyRollbackNode          = "rollback_node"
)
