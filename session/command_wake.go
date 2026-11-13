package session

import (
	"context"

	"crude/collaboration"
)

// humanInputWake closes when a human message addressed to agentID is waiting
// for the agent to read it, including one sent just before the command
// started. A blocking command then returns early as a background handle, so a
// shell sleep or slow build never keeps the user's message from the agent.
// The command itself keeps running. Cancel ctx to stop observing.
func (s *Session) humanInputWake(ctx context.Context, agentID string) <-chan struct{} {
	store := s.CollaborationStore()
	if store == nil || agentID == "" {
		return nil
	}
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
			case _, open := <-notifications:
				if !open {
					return
				}
			}
		}
	}()
	return wake
}
