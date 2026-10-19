package shellport

import (
	"context"
	"time"

	"crude/fileio"
	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/workspace"
)

// Agents is the agent inventory, turn control and blocking-prompt surface.
type Agents interface {
	// AgentMgr is the agent tree and flat index (replicated state holder).
	AgentMgr() *workspace.AgentManager
	// PaneMgr is the sub-pane display state (replicated state holder).
	PaneMgr() *workspace.PaneManager
	// AdvanceAgentState advances domain-owned lifecycle transitions. A consumer
	// may choose any presentation cadence without governing accepted semantic
	// evidence.
	AdvanceAgentState(now time.Time) workspace.AgentReduction
	// AgentActivity projects one owner's lifecycle and conversation without I/O or
	// inferring status from the presence of a rendered leaf.
	AgentActivity(agentID string) types.AgentActivityView
	// AgentModelIdentity is the current configuration for a root and the producing
	// identity for a retained child. It never borrows another tab's active
	// backend.
	AgentModelIdentity(agentID string) types.TurnIdentity
	// AgentTurnActive reports the backend lifecycle for the session owning
	// agentID.
	AgentTurnActive(agentID string) bool
	// UsageSummary returns one explicitly scoped, non-overlapping rollup.
	UsageSummary(agentID string, scope types.UsageScope) (types.UsageSummary, bool)
	// YoloForAgent reports the owning session's current live safety policy.
	YoloForAgent(agentID string) bool
	// SetYoloForAgent updates the owning session's live safety policy.
	SetYoloForAgent(agentID string, enabled bool) bool
	// ApplyAgentEvent is the single semantic ingress for already-owned backend
	// events. It never uses focus, terminal width, a renderer or a client's clock.
	// Identity-free service events are admitted only for existing owners, except
	// explicit child registration under an existing parent.
	ApplyAgentEvent(evt workspace.AgentEvent) workspace.AgentReduction
	// ApplyAgentStatusEvent reduces an accepted agent event into workspace status.
	// Call after applying the event to the owning agent state; ingress owns
	// identity and stale-upload rejection. Like AgentManager, it requires
	// serialized admission.
	ApplyAgentStatusEvent(evt workspace.AgentEvent)
	// AppendAgentMessages admits neutral evidence and optionally normalizes a
	// cold stopped transcript. UI projection helpers cannot mutate Conversation
	// directly.
	AppendAgentMessages(id string, messages []workspace.Message, stopped bool) bool
	// RegisterAgentRoot establishes inventory independently of any rendered shell.
	// Workspace lifecycle and all methods in this file require serialized callers.
	RegisterAgentRoot(id string) *workspace.AgentNode
	// ForgetAgentInventory removes a terminated subtree from the semantic owner.
	// Call only after CloseAgent has routed cancellation with its ownership
	// intact.
	ForgetAgentInventory(id string)
	// SyncAgentStatusInventory reconciles semantic lifecycle facts only. Selection
	// and visibility are deliberately absent from shared status authority.
	SyncAgentStatusInventory()
	// CancelAgent requests cancellation for one exact runtime generation.
	// Submission is not proof the control applied; the owner publishes the
	// authoritative outcome.
	CancelAgent(agentID string)
	// CloseAgent stops backend work for agentID before the TUI removes its state.
	// For root agents it also persists a recoverable registry record (Register +
	// SyncSessionMeta + CloseSession) so the closed session remains listable and
	// resumable. The session's own DB file is left intact.
	CloseAgent(agentID string)
	// BeginAgentCancellation publishes the stopping boundary independently of a
	// shell. Execution cancellation is separately routed to the exact backend.
	BeginAgentCancellation(id string) bool
	// SteerAgent publishes genuine human steering to one exact active run. Durable
	// delivery, epoch fencing and context admission are owned by Collaboration.
	SteerAgent(agentID string, input provider.UserInput) bool
	// ResolveQuestion sends formatted question answers to the owning orchestrator.
	ResolveQuestion(agentID, blockID string, answers, questions map[string]string, cancelled bool) bool
	// RejectQuestion reports a frontend validation failure without attributing it
	// to the user. The model receives actionable feedback and can retry the tool.
	RejectQuestion(agentID, blockID, detail string) bool
	// ResolveConfirmation sends a request-correlated permission response to the
	// owning orchestrator.
	ResolveConfirmation(agentID, blockID, choice string) bool
	// RetryAgentRecovery captures an exact owner in the UI and does its durable
	// resume work off Update. It never switches tabs or consumes the composer.
	PrepareAgentRecovery(agentID, incidentID string, generation uint64) func() error
	PrepareCancelAgentRecovery(agentID, incidentID string, generation uint64) func() error
	// RejectAgentRecoveryCancellation restores only the incident whose Stop was
	// rejected. A replaced recovery or newer turn is never changed by a late
	// reply.
	RejectAgentRecoveryCancellation(id, incident string)
	PrepareAsyncCommandTermination(agentID string, ref fileio.AsyncCmdRef) (func() error, error)
	// PrepareCompactForAgent registers lifecycle ownership synchronously so the
	// TUI can paint and cancel the operation before its tea.Cmd begins I/O.
	PrepareCompactForAgent(agentID string) (session.PreparedCompaction, types.CompactResultMsg)
	// HydrateSubAgents rebuilds completed review and unfinished recovery surfaces
	// under the root. Hydration never revives execution.
	HydrateSubAgents(parentID string) []string
	// PrepareAgentInputAdmission reserves an exact owner/generation without I/O.
	// Frontends call it while handling the submit gesture, then run the matching
	// commit in a command before projecting or clearing the accepted draft.
	PrepareAgentInputAdmission(agentID string, input provider.UserInput, draftRevision uint64) (*workspace.InputAdmissionIntent, error)
	// CommitAgentInputAdmission performs the durable publication off the UI
	// thread.
	CommitAgentInputAdmission(ctx context.Context, intent *workspace.InputAdmissionIntent) workspace.InputAdmissionResult
	// ApplyAgentInputAdmission commits the visible accepted input on the same
	// owner as runtime output. Only the originating client consumes its draft
	// afterward.
	ApplyAgentInputAdmission(intent *workspace.InputAdmissionIntent, result workspace.InputAdmissionResult, input provider.UserInput, labels []string) bool
	PrepareSkillInput(ctx context.Context, agentID, id, arguments string) (provider.UserInput, error)
}
