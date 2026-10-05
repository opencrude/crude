package workspace

import (
	"crude/sessiondb"
	"crude/types"
	"crude/types/recovery"
)

// RecoveryMessage is the one live/hydrated presentation of a retained incident.
// The runtime owns its lifecycle; this projection never schedules a retry.
func RecoveryMessage(status recovery.Status) Message {
	copy := status
	timestamp := status.StartedAt
	if timestamp.IsZero() {
		timestamp = status.UpdatedAt
	}
	return Message{
		InternalID: status.ID, Role: MessageRoleStatus, Content: status.Detail,
		Time: timestamp, TurnGeneration: status.Generation,
		LifecycleOperationID: status.ID, LifecyclePhase: "recovery_" + string(status.State),
		LifecycleRetryAttempt: status.Attempt, LifecycleRetryAt: status.RetryAt,
		Recovery: &copy,
	}
}

// UpdateRecovery replaces one stable row, rather than adding a diagnostic for
// each failed attempt. A later incident keeps its own immutable identity.
func (c *Conversation) UpdateRecovery(status recovery.Status) bool {
	if c == nil || status.ID == "" {
		return false
	}
	message := c.byID[status.ID]
	if message == nil {
		c.AppendMessage(RecoveryMessage(status))
		return true
	}
	if message.Recovery != nil && *message.Recovery == status {
		return false
	}
	*message = RecoveryMessage(status)
	c.lastRecovery = status
	c.version++
	return true
}

func mergeRecoveryViews(views []ChatMessageView, records []recovery.Status) []ChatMessageView {
	byID := make(map[string]int, len(views))
	for index, view := range views {
		byID[view.InternalID] = index
	}
	for _, record := range records {
		message := RecoveryMessage(record)
		if index, exists := byID[record.ID]; exists {
			views[index] = message
		} else {
			byID[record.ID] = len(views)
			views = append(views, message)
		}
	}
	return views
}

// RetryAgentRecovery captures an exact owner in the UI and does its durable
// resume work off Update. It never switches tabs or consumes the composer.
func (w *Workspace) PrepareAgentRecovery(agentID, incidentID string, generation uint64) func() bool {
	if owner := w.sessionForAgent(agentID); owner != nil && owner.backend != nil {
		backend := owner.backend
		return func() bool { return backend.RetryRecoveryFor(agentID, incidentID, generation) }
	}
	return nil
}

func (w *Workspace) PrepareCancelAgentRecovery(agentID, incidentID string, generation uint64) func() bool {
	if owner := w.sessionForAgent(agentID); owner != nil && owner.backend != nil {
		backend := owner.backend
		return func() bool { return backend.CancelRecoveryFor(agentID, incidentID, generation) }
	}
	return nil
}

func (a *AgentState) RecoveryStatus() recovery.Status {
	status := a.Recovery
	if a.Messages != nil && (status.ID == "" || a.Messages.lastRecovery.UpdatedAt.After(status.UpdatedAt)) {
		status = a.Messages.lastRecovery
	}
	if status.Generation > 0 && status.Generation < a.TurnIdentity.Generation {
		return recovery.Status{}
	}
	return status
}

func (w *Workspace) hydrateRecoveryViews(views []ChatMessageView, owner *Session) []ChatMessageView {
	if owner == nil || owner.DB == nil {
		return views
	}
	records, err := sessiondb.LoadRecoveryRecords(owner.DB, owner.SessionID)
	if err != nil {
		w.Logger.Warn("RECOVERY", "hydrate_failed", "error", err.Error())
		return views
	}
	return mergeRecoveryViews(views, records)
}

func recoveryPhase(status recovery.Status) (types.AgentPhase, bool) {
	switch status.State {
	case recovery.Recovering:
		return types.AgentRecovering, false
	case recovery.NeedsAttention:
		return types.AgentNeedsAttention, true
	case recovery.Interrupted:
		return types.AgentInterrupted, true
	default:
		return types.AgentPreparing, false
	}
}
