package collaboration

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLifecycleCommitsNotifyObserversWithoutBoardPublication(t *testing.T) {
	for _, mutation := range []string{"register", "start", "transition", "finish", "availability", "offline", "unresponsive", "heartbeat-recovery"} {
		t.Run(mutation, func(t *testing.T) {
			store, _ := openTestStore(t)
			root, _ := registerTestAgents(t, store)
			run, err := store.StartRun(t.Context(), StartRunRequest{AgentID: root.ID, RuntimeEpoch: root.RuntimeEpoch, Origin: OriginHuman})
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "heartbeat-recovery" {
				if _, err := store.MarkUnresponsiveBefore(t.Context(), time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			before, err := store.Page(t.Context(), PageQuery{Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			notifications, unsubscribe := store.Subscribe()
			defer unsubscribe()
			agentID := root.ID
			switch mutation {
			case "register":
				agentID = "new-observed-agent"
				_, err = store.RegisterAgent(t.Context(), AgentRegistration{ID: agentID, SessionID: "new-session", OwnerInstance: "new-owner"})
			case "start":
				agentID = "child"
				run, err = store.StartRun(t.Context(), StartRunRequest{AgentID: agentID, RuntimeEpoch: 1, Origin: OriginHuman})
			case "transition":
				run, err = store.TransitionRun(t.Context(), run.ID, root.ID, root.RuntimeEpoch, run.Version, RunWaitingEvent)
			case "finish":
				run, err = store.FinishRun(t.Context(), FinishRunRequest{RunID: run.ID, AgentID: root.ID,
					RuntimeEpoch: root.RuntimeEpoch, ExpectedVersion: run.Version, State: RunSucceeded})
			case "availability":
				_, err = store.SetAgentState(t.Context(), root.ID, root.RuntimeEpoch, AgentSuspended)
			case "offline":
				err = store.DetachAgent(t.Context(), root.ID, "process-a", root.RuntimeEpoch)
			case "unresponsive":
				_, err = store.MarkUnresponsiveBefore(t.Context(), time.Now().Add(time.Hour))
			case "heartbeat-recovery":
				err = store.Heartbeat(t.Context(), root.ID, "process-a", root.RuntimeEpoch)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-notifications:
			default:
				t.Fatal("committed lifecycle mutation did not notify observers")
			}
			// A wake is post-commit and observes the actual authority. It is not
			// a synthetic directed report/outcome and consumes no Board sequence.
			agent, err := store.Agent(t.Context(), agentID)
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "finish" {
				current, err := store.Run(t.Context(), run.ID)
				if err != nil || current.State != RunSucceeded || agent.CurrentRunID != "" {
					t.Fatal("notification preceded terminal commitment", err)
				}
			}
			after, err := store.Page(t.Context(), PageQuery{Since: before.Snapshot, Limit: 1})
			if err != nil || len(after.Events) != 0 || after.Snapshot != before.Snapshot {
				t.Fatal("lifecycle wake fabricated a Board publication", err)
			}
			// Failed writes and ordinary reads must not start a wake/re-read loop.
			if _, err := store.Agent(t.Context(), agentID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.FinishRun(t.Context(), FinishRunRequest{RunID: run.ID, AgentID: root.ID,
				RuntimeEpoch: root.RuntimeEpoch, ExpectedVersion: 0, State: RunSucceeded}); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid terminal precondition was accepted", err)
			}
			select {
			case <-notifications:
				t.Fatal("read or rejected lifecycle mutation notified observers")
			default:
			}
		})
	}
}

func TestUnchangedHeartbeatDoesNotWakeObservers(t *testing.T) {
	store, _ := openTestStore(t)
	root, _ := registerTestAgents(t, store)
	notifications, unsubscribe := store.Subscribe()
	defer unsubscribe()
	if err := store.Heartbeat(context.Background(), root.ID, "process-a", root.RuntimeEpoch); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notifications:
		t.Fatal("timestamp-only heartbeat woke lifecycle observers")
	default:
	}
}
