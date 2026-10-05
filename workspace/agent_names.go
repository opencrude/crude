package workspace

import "crude/types"

// AgentActivity projects one owner's lifecycle and conversation without I/O or
// inferring status from the presence of a rendered leaf.
func (w *Workspace) AgentActivity(agentID string) types.AgentActivityView {
	node := w.AgentMgr.Get(agentID)
	if node == nil {
		return types.AgentActivityView{}
	}
	view := types.AgentActivityView{Summary: AgentDisplayLabel(node)}
	if w.Status != nil {
		view.Status, _ = w.Status.Agent(agentID)
	}
	view.Status.Ref.Kind, view.Status.Ref.ID = types.ResourceAgent, agentID
	if node.State != nil {
		view.Recovery = node.State.RecoveryStatus()
		switch view.Recovery.State {
		case "recovering", "needs_attention", "interrupted":
			view.Status.Phase, view.Status.NeedsAction = recoveryPhase(view.Recovery)
			view.Status.Detail, view.Status.RetryAt = view.Recovery.Summary, view.Recovery.RetryAt
		}
		view.Active = node.State.IsActive()
		if node.State.ActiveCompaction.OperationID != "" {
			view.CompactionPhase = node.State.ActiveCompaction.Phase
			if node.State.Cancelling {
				view.CompactionPhase = "compaction_cancelling"
			}
		} else if !view.Active && node.State.Messages != nil {
			// The last durable boundary is the authority until another turn starts.
			// A completed manual /compact is an operation result, not plan progress.
			if last := node.State.Messages.lastNonPending(); last != nil &&
				last.Role == MessageRoleStatus && last.LifecycleTrigger == "manual" &&
				last.LifecycleOperationID != "" {
				switch last.LifecyclePhase {
				case "compaction_completed", "compaction_cancelled", "compaction_failed", "compaction_interrupted":
					view.CompactionPhase = last.LifecyclePhase
				}
			}
		}
		view.TideForward = node.State.ReasoningMode.IsSummary()
		if brief := node.State.Messages.Brief(); brief != "" {
			view.Summary = brief
		}
		if view.Status.Phase == types.AgentRetrying {
			if retry := node.State.Messages.lastNonPending(); retry != nil && retry.LifecyclePhase == "retrying" {
				view.RetryAttempt, view.RetryLimit, view.RetryReason = retry.LifecycleRetryAttempt, retry.LifecycleRetryLimit, retry.Content
				view.Status.RetryAt = retry.LifecycleRetryAt
			}
		}
		if view.Status.Phase == types.AgentThinking {
			// A promoted body already has its own live surface. Only inline
			// thoughts need a compact preview in the fixed activity strip.
			if thought, ok := node.State.Messages.ActiveThinking(); ok && thought.ThinkingDisplay.Kind.HasCompactPreview() {
				view.Thinking = thought.Content
				view.ThinkingID = thought.InternalID
				view.ThinkingDisplay = thought.ThinkingDisplay
				view.TideForward = view.TideForward || thought.ThinkingDisplay.Kind.IsSummary()
			}
		}
	}
	if usage, ok := w.UsageSummary(agentID, types.UsageThisAgent); ok {
		view.SuccessfulCalls, view.CallsKnown = usage.Turns, usage.TurnsKnown
	} else {
		view.CallsKnown = true
	}
	if owner := w.sessionForAgent(agentID); owner != nil && owner.backend != nil {
		view.Plan = owner.backend.PlanActivity()
		if node.Parent == nil {
			if summary := owner.backend.SummaryTitle(); summary != "" {
				view.Summary = summary
			}
		}
	}
	return view
}

// AgentModelIdentity is the current configuration for a root and the producing
// identity for a retained child. It never borrows another tab's active backend.
func (w *Workspace) AgentModelIdentity(agentID string) types.TurnIdentity {
	node := w.AgentMgr.Get(agentID)
	if node == nil {
		return types.TurnIdentity{}
	}
	if node.Parent == nil {
		if owner := w.sessionForAgent(agentID); owner != nil && owner.backend != nil {
			return owner.backend.ModelIdentity()
		}
	}
	identity := types.TurnIdentity{ModelID: node.Model}
	if node.State != nil {
		identity = node.State.TurnIdentity
		if node.Model != "" {
			identity.ModelID = node.Model
		}
	}
	return identity
}

// AgentNameFor resolves the durable identity of a root session. Presentation
// never allocates names: reopening a tab must retain its original identity.
func (w *Workspace) AgentNameFor(agentID string) string {
	if owner := w.sessionForAgent(agentID); owner != nil && owner.backend != nil {
		return owner.backend.AgentName()
	}
	return ""
}
