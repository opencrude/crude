package workspace

import (
	"crude/types/recovery"
	"crude/types/statuscenter"
	"time"
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
			status = statuscenter.AgentStatusSnapshot{Ref: statuscenter.ResourceRef{Kind: statuscenter.ResourceAgent, ID: node.ID}, Phase: statuscenter.AgentIdle, Detail: "Idle"}
		}
		status.Label, status.ReadOnly = w.agentIdentityLabel(node), state.Completed
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
		question := blocked && (prompt.IsQuestion || status.Phase == statuscenter.AgentQuestion)
		approval := blocked && !question && !w.YoloForAgent(node.ID)
		status.NeedsAction = question || approval
		incident := state.RecoveryStatus()
		switch {
		case state.Cancelling:
			status = StoppingStatus(status)
		case question:
			status.Phase, status.Detail = statuscenter.AgentQuestion, "Needs answer"
		case approval:
			status.Phase, status.Detail = statuscenter.AgentApproval, "Needs approval"
		case incident.State == recovery.NeedsAttention:
			status.Phase, status.Detail, status.NeedsAction = statuscenter.AgentNeedsAttention, incident.Summary, true
		case incident.State == recovery.Interrupted:
			status.Phase, status.Detail, status.NeedsAction = statuscenter.AgentInterrupted, incident.Summary, true
		case incident.State == recovery.Recovering && incident.CanRetry():
			status.Phase, status.Detail, status.RetryAt = statuscenter.AgentRecovering, incident.Summary, incident.RetryAt
		case state.Completed:
			status.Phase, status.Detail = agentTerminalPhase(state.Status)
			status.NeedsAction = false
		case !state.IsActive() && status.Phase != statuscenter.AgentCompleted && status.Phase != statuscenter.AgentCancelled && status.Phase != statuscenter.AgentFailed && status.Phase != statuscenter.AgentCompactionResult:
			status.Phase, status.Detail = statuscenter.AgentIdle, "Idle"
		case state.IsActive() && (status.Phase == statuscenter.AgentApproval || status.Phase == statuscenter.AgentQuestion):
			status.Phase, status.Detail = statuscenter.AgentThinking, "Working"
		case state.IsActive() && !liveAgentPhase(status.Phase):
			// Accepted input starts the turn at admission. A phase left by the
			// previous turn (Completed, Failed, Idle...) must not keep claiming
			// nothing is happening until the run's first event arrives.
			status.Phase, status.Detail = statuscenter.AgentPreparing, "Preparing context"
			status.StartedAt = time.Now()
			status.RequestID = ""
			status.UploadedBytes, status.TotalBytes, status.ProgressKnown, status.Progress = 0, 0, false, 0
		}
		w.Status.SetAgent(status)
	}
}

// liveAgentPhase reports phases that describe a turn in progress.
func liveAgentPhase(phase statuscenter.AgentPhase) bool {
	switch phase {
	case statuscenter.AgentOpening, statuscenter.AgentPreparing, statuscenter.AgentUploading, statuscenter.AgentWaiting, statuscenter.AgentCompacting,
		statuscenter.AgentThinking, statuscenter.AgentResponding, statuscenter.AgentTool, statuscenter.AgentChildren, statuscenter.AgentRetrying,
		statuscenter.AgentRecovering, statuscenter.AgentCancelling:
		return true
	}
	return false
}
