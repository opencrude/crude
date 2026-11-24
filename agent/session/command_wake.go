package session

import (
	"context"

	"crude/agent/collaboration"
)

// humanInputWake closes when a human message addressed to agentID is waiting
// for the agent to read it, including one sent just before the command
// started, or when the human asked to compact that agent's context. A blocking
// command then returns early as a background handle, so a shell sleep or slow
// build never keeps the user's message or /compact from the agent. The
// command itself keeps running. Cancel ctx to stop observing.
func (s *Session) humanInputWake(ctx context.Context, agentID string) <-chan struct{} {
	store := s.CollaborationStore()
	if store == nil || agentID == "" {
		return nil
	}
	compaction := s.compactionWake(agentID)
	wake := make(chan struct{})
	notifications, unsubscribe := store.Subscribe()
	go func() {
		defer unsubscribe()
		for {
			// Subscribing before the first read closes the read/subscribe gap.
			page, err := store.Page(ctx, collaboration.PageQuery{
				RecipientID: agentID, Unadmitted: true, Kind: collaboration.EventHumanSteering, Limit: 1,
			})
			if err == nil && len(page.Events) > 0 {
				close(wake)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-compaction:
				close(wake)
				return
			case _, open := <-notifications:
				if !open {
					return
				}
			}
		}
	}()
	return wake
}

// compactionWake is closed while the agent's running turn holds a human
// compaction request. Waits end on it so the next step boundary compacts
// immediately instead of after the wait's own timeout.
func (s *Session) compactionWake(agentID string) <-chan struct{} {
	if s == nil || agentID == "" {
		return nil
	}
	s.orchMu.Lock()
	orchestrator := s.orchestrators[agentID]
	s.orchMu.Unlock()
	return orchestrator.compactionPending()
}
