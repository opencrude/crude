package coreprotocol

const SubmitControlInput = "agent.submit_control"

// Attribution is recovered from the original admitted control, not the shell.
type ControlInputRequest struct {
	ControlRequestID string   `json:"control_request_id"`
	SessionID        string   `json:"session_id"`
	AgentID          string   `json:"agent_id"`
	DraftEpoch       uint64   `json:"draft_epoch"`
	DraftRevision    uint64   `json:"draft_revision"`
	Text             string   `json:"text"`
	ReferenceIDs     []string `json:"reference_ids,omitempty"`
}
