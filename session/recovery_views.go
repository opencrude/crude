package session

import (
	"encoding/json"
	"slices"

	"crude/agent"
	"crude/sessiondb"
)

// PendingSubAgentSnapshots exposes parked/passively restored child work without
// opening a DB, launching a child, or mislabelling it as a completed result.
func (s *Session) PendingSubAgentSnapshots() []SubAgentSnapshot {
	s.recoveryMu.Lock()
	checkpoints := make([]sessiondb.RecoveryCheckpoint, 0, len(s.pendingRecoveries))
	for _, checkpoint := range s.pendingRecoveries {
		if len(checkpoint.OwnerData) > 0 {
			checkpoints = append(checkpoints, checkpoint)
		}
	}
	s.recoveryMu.Unlock()
	slices.SortFunc(checkpoints, func(a, b sessiondb.RecoveryCheckpoint) int {
		return a.Status.StartedAt.Compare(b.Status.StartedAt)
	})
	var snapshots []SubAgentSnapshot
	for _, checkpoint := range checkpoints {
		var owner childRecoveryOwner
		if json.Unmarshal(checkpoint.OwnerData, &owner) != nil || string(owner.Spec.ID) != checkpoint.Status.AgentID {
			continue
		}
		status := checkpoint.Status
		snapshot := SubAgentSnapshot{
			Result: agent.SubAgentResult{AgentID: owner.Spec.ID, SessionID: string(owner.Spec.ID),
				Name: owner.Spec.Name, Category: owner.Spec.Category},
			Model: owner.Spec.Model, Recovery: &status,
		}
		archive := recoveryArchiveMessages(reconcileInterruptedTools(checkpoint.Archive, checkpoint.InFlightTools...))
		args := toolCallArgsIndex(archive)
		names := make(map[string]string)
		for _, message := range archive {
			for _, call := range message.ToolCalls {
				names[call.ID] = call.Function.Name
			}
		}
		for i, message := range archive {
			meta := sessiondb.DisplayMeta{DisplayRole: message.Role, TurnGeneration: status.Generation,
				ProviderID: owner.Spec.Provider, AccountID: owner.Spec.AccountID, ModelID: owner.Spec.Model}
			if message.Role == "user" && i == 0 {
				meta.DisplayRole = "assignment"
			}
			if isProviderControlMessage(message) {
				meta.ContextOnly, meta.DisplayRole = true, "hidden_runtime_context"
			}
			if message.Role == "tool" {
				meta.ToolName, meta.ToolArgs = names[message.ToolCallID], args[message.ToolCallID]
				if message.ToolError {
					meta.ToolStatus = "error"
				}
			}
			meta = canonicalizeDisplayMeta(message, meta, args[message.ToolCallID], i, status.StartedAt)
			snapshot.Messages = append(snapshot.Messages, sessiondb.MessageWithMeta{
				Msg: message, Meta: meta, Turn: 1, CreatedAt: status.StartedAt.UnixNano(),
			})
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}
