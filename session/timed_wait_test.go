package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"crude/collaboration"
	"crude/tools"
)

func TestTimedWaitLeavesAddressedEventsForDelivery(t *testing.T) {
	for _, kind := range []string{"human", "note", "command", "outcome"} {
		t.Run(kind, func(t *testing.T) {
			s, store, recipient, sender := collaborationContractFixture(t)
			event := collaborationWaitCommunication(t, store, recipient, sender, kind)()
			ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: recipient.ID})
			result, err := s.buildToolFn(false, nil, nil)(ctx, "wait", map[string]any{"seconds": 30.0})
			if err != nil || !strings.Contains(result.Content, "ended early") {
				t.Fatalf("wait result = %q, %v", result.Content, err)
			}
			page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: recipient.ID, Unadmitted: true})
			if err != nil || len(page.Events) != 1 || page.Events[0].ID != event.ID {
				t.Fatalf("wait changed delivery: %+v, %v", page, err)
			}
		})
	}
}

func TestTimedWaitTimeoutAndCancellation(t *testing.T) {
	s, store, recipient, other := collaborationContractFixture(t)
	collaborationWaitCommunication(t, store, other, recipient, "note")()
	ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: recipient.ID})
	run := s.buildToolFn(false, nil, nil)
	start := time.Now()
	result, err := run(ctx, "wait", map[string]any{"seconds": 0.05})
	if err != nil || result.Content != "Waited 0.1 seconds." || time.Since(start) < 40*time.Millisecond {
		t.Fatalf("timeout result = %q, %v after %v", result.Content, err, time.Since(start))
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = run(cancelCtx, "wait", map[string]any{"seconds": 30.0})
	if err == nil {
		t.Fatal("cancelled turn unexpectedly executed wait")
	}
}

func TestTimedWaitWakesForNewCommandOutcome(t *testing.T) {
	s, store, recipient, sender := collaborationContractFixture(t)
	publish := collaborationWaitCommunication(t, store, recipient, sender, "command")
	ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: recipient.ID})
	type answer struct {
		content string
		err     error
	}
	done := make(chan answer, 1)
	go func() {
		result, err := s.buildToolFn(false, nil, nil)(ctx, "wait", map[string]any{"seconds": 30.0})
		done <- answer{result.Content, err}
	}()
	event := publish()
	select {
	case result := <-done:
		if result.err != nil || !strings.Contains(result.content, "ended early") {
			t.Fatalf("wait result = %q, %v", result.content, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("command outcome did not interrupt the timed wait")
	}
	page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: recipient.ID, Unadmitted: true})
	if err != nil || len(page.Events) != 1 || page.Events[0].ID != event.ID {
		t.Fatalf("wait changed delivery: %+v, %v", page, err)
	}
}
