package coreclient

import (
	"fmt"
	"maps"
	"slices"

	"crude/coreprotocol"
)

// ApplyPublication preflights the complete candidate inventory and every
// transcript before mutating any retained session, node or conversation.
func (v *WorkspaceView) ApplyPublication(update coreprotocol.WorkspaceUpdate) ([]string, error) {
	if update.Snapshot != nil {
		return nil, fmt.Errorf("%w: snapshot requires a reset boundary", ErrResync)
	}
	changes := update.Changes
	if changes.Revision < v.revision {
		return nil, fmt.Errorf("%w: stale publication", ErrResync)
	}
	changes.Agents = slices.Clone(changes.Agents)
	changes.Removed = v.expandRemoved(changes.Removed)
	candidate := maps.Clone(v.agents)
	for _, id := range changes.Removed {
		delete(candidate, id)
	}
	seen := make(map[string]bool, len(changes.Agents)+len(update.Conversations))
	for _, delta := range changes.Agents {
		if seen[delta.State.ID] {
			return nil, fmt.Errorf("%w: duplicate agent", ErrResync)
		}
		seen[delta.State.ID] = true
		candidate[delta.State.ID] = delta.State
	}
	for _, item := range update.Conversations {
		state, ok := candidate[item.AgentID]
		if !ok || seen[item.AgentID] {
			return nil, fmt.Errorf("%w: unknown or duplicate conversation %s", ErrResync, item.AgentID)
		}
		seen[item.AgentID] = true
		state.ConversationRevision, state.LastTokenTime = item.Delta.Revision, item.LastTokenTime
		changes.Agents = append(changes.Agents, coreprotocol.AgentDelta{State: state, Conversation: item.Delta})
		candidate[item.AgentID] = state
	}
	if err := v.validateChanges(changes); err != nil {
		return nil, err
	}
	sessions := make(map[string]string, len(v.Sessions))
	if update.Sessions != nil {
		for _, s := range *update.Sessions {
			if s.ID == "" {
				return nil, fmt.Errorf("%w: empty session", ErrResync)
			}
			if _, duplicate := sessions[s.ID]; duplicate {
				return nil, fmt.Errorf("%w: duplicate session", ErrResync)
			}
			sessions[s.ID] = s.AgentID
		}
	} else {
		for _, s := range v.Sessions {
			sessions[s.SessionID] = s.AgentID
		}
	}
	for _, a := range candidate {
		if a.SessionID != "" {
			if _, ok := sessions[a.SessionID]; !ok {
				return nil, fmt.Errorf("%w: missing candidate session for %s", ErrResync, a.ID)
			}
		}
	}
	for id, agentID := range sessions {
		if agentID != "" {
			if a, ok := candidate[agentID]; !ok || a.SessionID != id || a.ParentID != "" {
				return nil, fmt.Errorf("%w: invalid session root %s", ErrResync, agentID)
			}
		}
	}
	if update.Sessions != nil {
		v.installSessions(*update.Sessions)
	}
	return v.installChanges(changes)
}

// InstallPublicationSnapshot admits a new core revision domain without replacing
// surviving replica identities. Preflight happens before revision fences change.
func (v *WorkspaceView) InstallPublicationSnapshot(snapshot coreprotocol.WorkspaceSnapshot, newDomain bool) error {
	if !newDomain {
		return v.InstallSnapshot(snapshot)
	}
	if snapshot.ID != v.ID {
		return fmt.Errorf("%w: wrong workspace", ErrResync)
	}
	// Validate against an empty graph: a replacement core may legitimately have
	// lower workspace and transcript revisions, but not malformed state.
	if _, err := NewWorkspaceView(nil, snapshot); err != nil {
		return err
	}
	for _, agent := range snapshot.Agents {
		if old, ok := v.agents[agent.ID]; ok && old.ParentID != agent.ParentID {
			return fmt.Errorf("%w: agent reparented", ErrResync)
		}
	}
	for _, owner := range v.Sessions {
		owner.ResetReplicaRevision()
	}
	v.revision = 0
	for _, node := range v.AgentMgr.AllNodes() {
		node.State.Messages.ResetReplicaRevision()
	}
	return v.InstallSnapshot(snapshot)
}
