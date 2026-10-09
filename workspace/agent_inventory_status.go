package workspace

import (
	"crude/types"
	"crude/types/recovery"
)

// SyncAgentStatusInventory reconciles semantic lifecycle facts only. Selection
// and visibility are deliberately absent from shared status authority.
func (w *Workspace) SyncAgentStatusInventory() {
	if w == nil || w.AgentMgr == nil || w.Status == nil {
		return
	}
	for _, node := range w.AgentMgr.AllNodes() {
		if node == nil || node.State == nil {
			continue
		}
		state := node.State
		status, exists := w.Status.Agent(node.ID)
		if !exists {
			status = types.AgentStatusSnapshot{Ref: types.ResourceRef{Kind: types.ResourceAgent, ID: node.ID}, Phase: types.AgentIdle, Detail: "Idle"}
		}
		status.Label, status.ReadOnly = node.Label, state.Completed
		if node.Model != "" {
			status.ModelID = node.Model
		}
		if owner := w.SessionForAgent(node.ID); owner != nil {
			status.Ref.SessionID = owner.SessionID
		}
		if node.Parent != nil {
			status.Ref.ParentID = node.Parent.ID
		}
		blocked := state.PendingBlockID != "" && state.PendingPermTag == ""
		prompt := w.agentPrompts[node.ID]
		question := blocked && (prompt.IsQuestion || status.Phase == types.AgentQuestion)
		approval := blocked && !question && !w.YoloForAgent(node.ID)
		status.NeedsAction = question || approval
		incident := state.RecoveryStatus()
		switch {
		case state.Cancelling:
			status.Phase, status.Detail, status.NeedsAction = types.AgentCancelling, "Stopping", false
		case question:
			status.Phase, status.Detail = types.AgentQuestion, "Needs answer"
		case approval:
			status.Phase, status.Detail = types.AgentApproval, "Needs approval"
		case incident.State == recovery.NeedsAttention:
			status.Phase, status.Detail, status.NeedsAction = types.AgentNeedsAttention, incident.Summary, true
		case incident.State == recovery.Interrupted:
			status.Phase, status.Detail, status.NeedsAction = types.AgentInterrupted, incident.Summary, true
		case incident.State == recovery.Recovering && incident.CanRetry():
			status.Phase, status.Detail, status.RetryAt = types.AgentRecovering, incident.Summary, incident.RetryAt
		case state.Completed:
			status.Phase, status.Detail = agentTerminalPhase(state.Status)
			status.NeedsAction = false
		case !state.IsActive() && status.Phase != types.AgentCompleted && status.Phase != types.AgentCancelled && status.Phase != types.AgentFailed && status.Phase != types.AgentCompactionResult:
			status.Phase, status.Detail = types.AgentIdle, "Idle"
		case state.IsActive() && (!exists || status.Phase == types.AgentIdle || status.Phase == types.AgentCompactionResult || status.Phase == types.AgentApproval || status.Phase == types.AgentQuestion):
			status.Phase, status.Detail = types.AgentThinking, "Working"
		}
		w.Status.SetAgent(status)
	}
}
