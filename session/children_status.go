package session

import (
	"context"
	"strings"

	"crude/collaboration"
	rootprompt "crude/prompt"
	"crude/provider"
)

// childrenStatusNotice returns a notice of the parent's children when their
// states changed since previous, and the new digest to compare against.
func (s *Session) childrenStatusNotice(ctx context.Context, parentID, previous string) (*provider.ChatMessage, string) {
	store := s.CollaborationStore()
	if store == nil {
		return nil, previous
	}
	children, err := store.ListAgents(ctx, collaboration.AgentQuery{ParentID: parentID, Limit: collaboration.MaxAgentLimit})
	if err != nil || len(children) == 0 {
		return nil, previous
	}
	statuses := make([]rootprompt.ChildStatus, 0, len(children))
	var digest strings.Builder
	for _, child := range children {
		state := childStateLabel(ctx, store, child)
		statuses = append(statuses, rootprompt.ChildStatus{Name: child.Name, Category: child.Category, State: state})
		digest.WriteString(child.ID + "=" + state + ";")
	}
	if digest.String() == previous {
		return nil, previous
	}
	notice := runtimeMessage(rootprompt.ChildrenStatus(statuses))
	return &notice, digest.String()
}

func childStateLabel(ctx context.Context, store *collaboration.Store, child collaboration.Agent) string {
	if child.Presence != collaboration.RuntimeAttached {
		if child.State == collaboration.AgentAvailable {
			return "idle"
		}
		return string(child.State)
	}
	if child.CurrentRunID == "" {
		return "idle"
	}
	run, err := store.Run(ctx, child.CurrentRunID)
	if err != nil {
		return "running"
	}
	switch run.State {
	case collaboration.RunWaitingHuman:
		return "waiting for the human (approval or answer)"
	case collaboration.RunWaitingEvent:
		return "waiting for a delivery"
	case collaboration.RunRunning, collaboration.RunQueued:
		return "working"
	default:
		return strings.ReplaceAll(string(run.State), "_", " ")
	}
}
