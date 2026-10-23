// Package coreprotocol defines the versioned, presentation-neutral core API.
// Requests never contain runtime pointers, callbacks, or database handles.
package coreprotocol

import (
	"encoding/json"
	"time"

	"crude/corewire"
	"crude/sessiondb"
	"crude/types"
	"crude/workspace"
)

const (
	Attach       = "workspace.attach"
	Detach       = "workspace.detach"
	Snapshot     = "workspace.snapshot"
	AgentChanges = "agent.changes"
	NewSession   = "session.create"
	OpenSession  = "session.open"
	// ForkSession copies an open session (SessionID) into a new one and opens it.
	ForkSession    = "session.fork"
	ListSessions   = "session.list"
	SubmitInput    = "agent.submit"
	SteerInput     = "agent.steer"
	Decide         = "agent.decide"
	CancelAgent    = "agent.cancel"
	TerminateAgent = "agent.terminate"
	GetReceipt     = "request.receipt"
	// SessionSnapshots returns the session set without agent transcripts.
	SessionSnapshots = "session.snapshots"
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
	ID       string                    `json:"id"`
	Revision uint64                    `json:"revision,omitempty"`
	Payload  json.RawMessage           `json:"payload,omitempty"`
	Error    *Fault                    `json:"error,omitempty"`
	Transfer *corewire.PayloadTransfer `json:"transfer,omitempty"`
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
	// Full is the complete redacted settings set with its revision; nil when
	// the session's runtime is not open.
	Full *SessionSettingsResult `json:"full,omitempty"`
	// ConnectionIssue explains a missing or removed connection; the transcript
	// stays readable while a shell offers explicit repair.
	ConnectionIssue string `json:"connection_issue,omitempty"`
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
	WorkspaceID  string   `json:"workspace_id"`
	BaseRevision uint64   `json:"base_revision"`
	Revision     uint64   `json:"revision"`
	Agents       []string `json:"agents,omitempty"`
	// Reductions are the accepted semantic reductions, in admission order. Every
	// attached shell presents the same effects; none re-runs the reducer.
	Reductions []workspace.AgentReduction `json:"reductions,omitempty"`
	// Notices are typed service notices outside the semantic reducer.
	Notices []workspace.AgentNotice `json:"notices,omitempty"`
	// Status reports that the workspace status center changed.
	Status bool `json:"status,omitempty"`
	// Settings reports changed application settings, overrides or accounts.
	Settings bool `json:"settings,omitempty"`
	// Sessions reports a changed session set or session settings.
	Sessions bool `json:"sessions,omitempty"`
	// LSP announces a new diagnostic, outline or server-health projection.
	LSP bool `json:"lsp,omitempty"`
	// Documents advertises changed live text or savepoints, never client cursors.
	Documents []string     `json:"documents,omitempty"`
	Files     []FileChange `json:"files,omitempty"`
	// Progress is request-scoped partial output, sent only to the requester.
	Progress        *Progress                     `json:"progress,omitempty"`
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
	// Manual compaction is independent of the last model turn. Stop must name
	// its exact operation so delayed cancellation cannot stop its replacement.
	OperationID string `json:"operation_id,omitempty"`
}
type InputRequest struct {
	AgentID       string   `json:"agent_id"`
	Text          string   `json:"text"`
	ReferenceIDs  []string `json:"reference_ids,omitempty"`
	ActivePaths   []string `json:"active_paths,omitempty"`
	DraftRevision uint64   `json:"draft_revision"`
	// DisplayText and ContextParts carry prepared input such as an exact
	// skill invocation; the core still attributes it as human input.
	DisplayText  string   `json:"display_text,omitempty"`
	ContextParts []string `json:"context_parts,omitempty"`
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
	// Reject reports a shell validation failure for a question payload; the
	// model gets actionable feedback and it is never attributed to the user.
	Reject string `json:"reject,omitempty"`
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

// Progress is advisory partial output for one in-flight request.
type Progress struct {
	RequestID string `json:"request_id"`
	Text      string `json:"text"`
}
