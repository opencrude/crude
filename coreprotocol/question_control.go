package coreprotocol

const QuestionControl = "agent.question_control"

// Attribution is recovered from the admitted shell request, never this payload.
type QuestionControlRequest struct {
	ControlRequestID string            `json:"control_request_id"`
	SessionID        string            `json:"session_id"`
	AgentID          string            `json:"agent_id"`
	BlockID          string            `json:"block_id"`
	Answers          map[string]string `json:"answers,omitempty"`
	Questions        map[string]string `json:"questions,omitempty"`
	Cancelled        bool              `json:"cancelled,omitempty"`
}
type QuestionControlResult struct {
	Accepted bool   `json:"accepted"`
	BlockID  string `json:"block_id"`
}
