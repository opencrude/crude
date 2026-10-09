// Package coreprotocol defines the versioned, presentation-neutral core API.
// Requests never contain runtime pointers, callbacks, or database handles.
package coreprotocol

import (
	"encoding/json"
	"time"

	"crude/sessiondb"
	"crude/types"
	"crude/workspace"
)

const (
	Attach         = "workspace.attach"
	Detach         = "workspace.detach"
	Snapshot       = "workspace.snapshot"
	AgentChanges   = "agent.changes"
	NewSession     = "session.create"
	OpenSession    = "session.open"
	ListSessions   = "session.list"
	SubmitInput    = "agent.submit"
	SteerInput     = "agent.steer"
	Decide         = "agent.decide"
	CancelAgent    = "agent.cancel"
	TerminateAgent = "agent.terminate"
	GetReceipt     = "request.receipt"
)

// Owner fences an attachment, not the selected tab in a shell. Epoch changes
// whenever that client establishes a new connection to a retained core.
type Owner struct {
	ClientID string `json:"client_id"`
	Epoch    uint64 `json:"epoch"`
}

type Request struct {
	ID          string          `json:"id"`
	Owner       Owner           `json:"owner"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Method      string          `json:"method"`
	Payload     json.RawMessage `json:"payload"`
}

type Fault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (f *Fault) Error() string { return f.Code + ": " + f.Message }

type Response struct {
	ID       string          `json:"id"`
	Revision uint64          `json:"revision,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	Error    *Fault          `json:"error,omitempty"`
}

// Receipt preserves uncertainty rather than retrying an effect after a lost
// response or core crash. Completed outcomes are immutable by request identity.
type Receipt struct {
	ClientID    string    `json:"client_id"`
	RequestID   string    `json:"request_id"`
	WorkspaceID string    `json:"workspace_id"`
	Method      string    `json:"method"`
	State       string    `json:"state"` // pending, completed, or uncertain
	Response    Response  `json:"response"`
	CreatedAt   time.Time `json:"created_at"`
}

type AttachRequest struct {
	Root string `json:"root"`
}
type AttachResult struct {
	WorkspaceID string            `json:"workspace_id"`
	Epoch       uint64            `json:"epoch"`
	Revision    uint64            `json:"revision"`
	View        WorkspaceSnapshot `json:"view"`
}

type SessionSnapshot struct {
	ID         string                `json:"id"`
	AgentID    string                `json:"agent_id"`
	CreatedAt  time.Time             `json:"created_at"`
	Settings   SessionSettings       `json:"settings"`
	References []sessiondb.Reference `json:"references"`
}

// WorkspaceSnapshot intentionally excludes services and credentials. It is a
// boundary snapshot, never a per-frame or per-token export.
type WorkspaceSnapshot struct {
	ID        string                    `json:"id"`
	Root      string                    `json:"root"`
	Username  string                    `json:"username"`
	StartedAt time.Time                 `json:"started_at"`
	Revision  uint64                    `json:"revision"`
	Agents    []workspace.AgentSnapshot `json:"agents"`
	Sessions  []SessionSnapshot         `json:"sessions"`
	Settings  ShellSettings             `json:"settings"`
}

// Only display preferences and connection identifiers belong in unsolicited
// snapshots. Editable secret-bearing entities need their own explicit contract.
type ShellSettings struct {
	Theme  types.ThemeSettings  `json:"theme"`
	Editor types.EditorSettings `json:"editor"`
	UI     types.UISettings     `json:"ui"`
	Font   types.FontSettings   `json:"font"`
}

type SessionSettings struct {
	MainAgent      string `json:"main_agent"`
	Model          string `json:"model"`
	ProviderID     string `json:"provider_id"`
	AccountID      string `json:"account_id"`
	ThinkingEffort string `json:"thinking_effort"`
	MultiAgent     *bool  `json:"multi_agent,omitempty"`
}

// Change advertises committed revisions. Readers fetch detached changed data
// using their prior cursor; no producer goroutine waits for a shell to repaint.
type Change struct {
	WorkspaceID     string                        `json:"workspace_id"`
	BaseRevision    uint64                        `json:"base_revision"`
	Revision        uint64                        `json:"revision"`
	Agents          []string                      `json:"agents,omitempty"`
	Resync          bool                          `json:"resync,omitempty"`
	Terminals       []string                      `json:"terminals,omitempty"`
	TerminalEffects *workspace.TerminalEffectsMsg `json:"terminal_effects,omitempty"`
}

type SessionRequest struct {
	SessionID string `json:"session_id"`
}
type AgentRequest struct {
	AgentID    string `json:"agent_id"`
	Generation uint64 `json:"generation"`
}
type InputRequest struct {
	AgentID       string   `json:"agent_id"`
	Text          string   `json:"text"`
	ReferenceIDs  []string `json:"reference_ids,omitempty"`
	ActivePaths   []string `json:"active_paths,omitempty"`
	DraftRevision uint64   `json:"draft_revision"`
}
type InputResult struct {
	Status  workspace.InputAdmissionStatus  `json:"status"`
	Receipt workspace.InputAdmissionReceipt `json:"receipt"`
	Error   string                          `json:"error,omitempty"`
}
type DecisionRequest struct {
	AgentID   string            `json:"agent_id"`
	BlockID   string            `json:"block_id"`
	Choice    string            `json:"choice,omitempty"`
	Answers   map[string]string `json:"answers,omitempty"`
	Questions map[string]string `json:"questions,omitempty"`
	Cancelled bool              `json:"cancelled,omitempty"`
}
type DecisionResult struct {
	Accepted bool `json:"accepted"`
}
type ReceiptRequest struct {
	RequestID string `json:"request_id"`
}
type AgentChangesRequest struct {
	Revisions map[string]uint64 `json:"revisions"`
}

type AgentDelta struct {
	State        workspace.AgentSnapshot     `json:"state"`
	Conversation workspace.ConversationDelta `json:"conversation"`
}

type AgentChangesResult struct {
	Revision uint64       `json:"revision"`
	Agents   []AgentDelta `json:"agents"`
	Removed  []string     `json:"removed"`
}
