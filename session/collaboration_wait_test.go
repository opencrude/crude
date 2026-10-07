package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"crude/collaboration"
	"crude/tools"
)

func TestCollaborationWaitReadyAndOrdinaryTimeoutAreObservations(t *testing.T) {
	s, store, parent, _ := collaborationContractFixture(t)
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		ID: "wait-ready-run", AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, Origin: collaboration.OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{
		RunID: run.ID, AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, ExpectedVersion: run.Version,
		State: collaboration.RunSucceeded, Outcome: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := s.waitForCollaboration(t.Context(), store, map[string]any{"agent_id": parent.ID, "run_id": run.ID})
	if err != nil {
		t.Fatal(err)
	}
	got := value.(collaborationWaitObservation)
	if got.Status != "ready" || got.Run == nil || got.Run.Version != finished.Version {
		t.Fatalf("ready observation = %+v", got)
	}

	started := time.Now()
	value, err = s.waitForCollaboration(t.Context(), store, map[string]any{
		"artifact_id": "artifact-not-yet-published", "timeout_seconds": 1,
	})
	if err != nil {
		t.Fatalf("ordinary timeout returned an error: %v", err)
	}
	got = value.(collaborationWaitObservation)
	if got.Status != "timeout_no_match" || time.Since(started) < 900*time.Millisecond || len(got.Unmet) != 1 {
		t.Fatalf("timeout observation = %+v elapsed=%s", got, time.Since(started))
	}
}

func TestCollaborationWaitTimeoutBoundsBlockedStoreRead(t *testing.T) {
	for _, test := range []struct {
		name     string
		identity bool
	}{
		{name: "initial directed-event fence", identity: true},
		{name: "condition observation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, store, parent, _ := collaborationContractFixture(t)
			s.db.SetMaxOpenConns(1)
			conn, err := s.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx := t.Context()
			if test.identity {
				ctx = context.WithValue(ctx, tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: parent.ID})
			}
			started := time.Now()
			value, err := s.waitForCollaboration(ctx, store, map[string]any{
				"artifact_id": "blocked-store-read", "timeout_seconds": 1,
			})
			elapsed := time.Since(started)
			if err != nil {
				t.Fatalf("blocked store read returned raw error after %s: %v", elapsed, err)
			}
			got := value.(collaborationWaitObservation)
			if got.Status != "timeout_no_match" || got.Source != "wait_control" || got.ObservedAt.IsZero() {
				t.Fatalf("bounded timeout observation=%+v", got)
			}
			if elapsed < 900*time.Millisecond || elapsed > 2*time.Second {
				t.Fatalf("blocked store read elapsed=%s, want one-second budget with controlled headroom", elapsed)
			}
			if got.Agent != nil || got.Run != nil || got.Artifact != nil || got.Event != nil {
				t.Fatalf("unavailable read spoofed fresh stored facts: %+v", got)
			}
		})
	}
}

func TestCollaborationWaitToolEntryReturnsReadyObservation(t *testing.T) {
	s, store, parent, _ := collaborationContractFixture(t)
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		ID: "wait-tool-entry-run", AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, Origin: collaboration.OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{
		RunID: run.ID, AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, ExpectedVersion: run.Version,
		State: collaboration.RunSucceeded, Outcome: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	actorCtx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{
		AgentID: parent.ID, ToolCallID: "wait-tool-entry",
	})
	result, handled, err := s.executeCollaborationTool(actorCtx, parent.ID, "agent_wait", map[string]any{"run_id": run.ID})
	if err != nil || !handled || !strings.Contains(result.Content, `"status":"ready"`) || !strings.Contains(result.Content, run.ID) {
		t.Fatalf("tool wait: handled=%t result=%s err=%v", handled, result.Content, err)
	}
}

func TestCollaborationWaitArtifactRevisionWakesAndSatisfiesAllConditions(t *testing.T) {
	s, store, parent, child := collaborationContractFixture(t)
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		ID: "wait-report-run", AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, Origin: collaboration.OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.PublishReport(t.Context(), collaboration.PublishReportRequest{
		ArtifactID: "wait-versioned-artifact", AuthorID: parent.ID, RunID: run.ID,
		RecipientIDs: []string{child.ID}, IdempotencyKey: "wait-report-v1",
		Kind: collaboration.ReportInterim, Summary: "First", Body: "First evidence.",
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan collaborationWaitObservation, 1)
	go func() {
		got, _ := s.waitForCollaboration(t.Context(), store, map[string]any{
			"agent_id": parent.ID, "run_id": run.ID, "artifact_id": first.Artifact.ID,
			"after_revision": 1, "timeout_seconds": 5,
		})
		done <- got.(collaborationWaitObservation)
	}()
	time.Sleep(30 * time.Millisecond)
	second, err := store.PublishReport(t.Context(), collaboration.PublishReportRequest{
		ArtifactID: first.Artifact.ID, AuthorID: parent.ID, RunID: run.ID,
		RecipientIDs: []string{child.ID}, IdempotencyKey: "wait-report-v2",
		Kind: collaboration.ReportFinal, Summary: "Final", Body: "Final evidence.", SupersedesRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{
		RunID: run.ID, AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, ExpectedVersion: run.Version,
		State: collaboration.RunSucceeded, Outcome: "completed",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.Status != "ready" || got.Artifact == nil || got.Artifact.Revision != second.Artifact.Revision || got.Run == nil || !got.Run.State.Terminal() {
			t.Fatalf("revision-ready observation=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("artifact revision and terminal run did not satisfy wait")
	}
}

func TestCollaborationWaitCallerInterruptionAndTerminalWithoutReport(t *testing.T) {
	s, store, parent, _ := collaborationContractFixture(t)
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		ID: "wait-terminal-run", AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, Origin: collaboration.OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(context.Canceled)
	value, err := s.waitForCollaboration(ctx, store, map[string]any{"run_id": run.ID})
	if err != nil {
		t.Fatal(err)
	}
	encoded := collaborationResult(value, nil).Content
	if !strings.Contains(encoded, `"status":"caller_interrupted"`) {
		t.Fatalf("caller interruption = %s", encoded)
	}

	if _, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{
		RunID: run.ID, AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, ExpectedVersion: run.Version,
		State: collaboration.RunFailed, Outcome: "needs repair",
	}); err != nil {
		t.Fatal(err)
	}
	value, err = s.waitForCollaboration(t.Context(), store, map[string]any{
		"agent_id": parent.ID, "run_id": run.ID, "artifact_id": "missing-final",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded = collaborationResult(value, nil).Content
	if !strings.Contains(encoded, `"status":"terminal_without_match"`) || !strings.Contains(encoded, `"artifact_revision"`) {
		t.Fatalf("terminal without report = %s", encoded)
	}
}

func TestCollaborationWaitDirectedEventDoesNotAdmitOrMarkRead(t *testing.T) {
	s, store, parent, child := collaborationContractFixture(t)
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		ID: "wait-board-run", AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, Origin: collaboration.OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	actorCtx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: child.ID})
	done := make(chan any, 1)
	errs := make(chan error, 1)
	go func() {
		value, waitErr := s.waitForCollaboration(actorCtx, store, map[string]any{"run_id": run.ID, "timeout_seconds": 5})
		if waitErr != nil {
			errs <- waitErr
			return
		}
		done <- value
	}()
	time.Sleep(30 * time.Millisecond)
	published, err := store.Publish(t.Context(), collaboration.PublishRequest{
		SenderID: parent.ID, IdempotencyKey: "wait-human-steer", RecipientIDs: []string{child.ID},
		Origin: collaboration.OriginHuman, Kind: collaboration.EventHumanSteering, Body: "Change direction now.",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errs:
		t.Fatal(err)
	case value := <-done:
		encoded := collaborationResult(value, nil).Content
		if !strings.Contains(encoded, `"status":"directed_event"`) || !strings.Contains(encoded, published.Event.ID) {
			t.Fatalf("directed observation = %s", encoded)
		}
	case <-time.After(time.Second):
		t.Fatal("directed event did not interrupt wait")
	}
	page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: child.ID})
	if err != nil {
		t.Fatal(err)
	}
	if page.HumanReadThrough != 0 {
		t.Fatalf("wait advanced human-read cursor to %d", page.HumanReadThrough)
	}
	pending, err := store.ReservePending(t.Context(), collaboration.ReserveRequest{RecipientID: child.ID, RuntimeEpoch: child.RuntimeEpoch})
	if err != nil || len(pending) != 1 || pending[0].Event.ID != published.Event.ID {
		t.Fatalf("wait mutated delivery admission: pending=%+v err=%v", pending, err)
	}
}

func TestCollaborationWaitRejectsWrongAgentAndStaleEpoch(t *testing.T) {
	s, store, parent, child := collaborationContractFixture(t)
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		ID: "wait-exact-run", AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, Origin: collaboration.OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.waitForCollaboration(t.Context(), store, map[string]any{"agent_id": child.ID, "run_id": run.ID}); err == nil || !strings.Contains(err.Error(), parent.ID) {
		t.Fatalf("wrong-agent error = %v", err)
	}
	if err := store.DetachAgent(t.Context(), parent.ID, "contract-owner", parent.RuntimeEpoch); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{
		ID: parent.ID, Name: parent.Name, SessionID: parent.SessionID, Category: parent.Category, OwnerInstance: "replacement-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.waitForCollaboration(t.Context(), store, map[string]any{"agent_id": recovered.ID, "run_id": run.ID}); err == nil || !strings.Contains(err.Error(), "stale runtime epoch") {
		t.Fatalf("stale-epoch error = %v", err)
	}
}

func TestCollaborationWaitSuspendedOfflineAndNeedsAttention(t *testing.T) {
	s, store, parent, child := collaborationContractFixture(t)
	if _, err := store.SetAgentState(t.Context(), child.ID, child.RuntimeEpoch, collaboration.AgentSuspended); err != nil {
		t.Fatal(err)
	}
	value, err := s.waitForCollaboration(t.Context(), store, map[string]any{"agent_id": child.ID})
	if err != nil || value.(collaborationWaitObservation).Status != "suspended" {
		t.Fatalf("suspended observation=%+v err=%v", value, err)
	}
	if _, err := store.SetAgentState(t.Context(), child.ID, child.RuntimeEpoch, collaboration.AgentAvailable); err != nil {
		t.Fatal(err)
	}
	if err := store.DetachAgent(t.Context(), child.ID, "contract-owner", child.RuntimeEpoch); err != nil {
		t.Fatal(err)
	}
	value, err = s.waitForCollaboration(t.Context(), store, map[string]any{"agent_id": child.ID})
	if err != nil || value.(collaborationWaitObservation).Status != "offline" {
		t.Fatalf("offline observation=%+v err=%v", value, err)
	}

	recovered, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{
		ID: child.ID, Name: child.Name, SessionID: child.SessionID, ParentID: parent.ID,
		Category: child.Category, OwnerInstance: "attention-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		ID: "wait-attention-run", AgentID: recovered.ID, RuntimeEpoch: recovered.RuntimeEpoch, Origin: collaboration.OriginPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	actorCtx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: parent.ID})
	done := make(chan collaborationWaitObservation, 1)
	go func() {
		got, _ := s.waitForCollaboration(actorCtx, store, map[string]any{"agent_id": recovered.ID, "run_id": run.ID, "timeout_seconds": 5})
		done <- got.(collaborationWaitObservation)
	}()
	time.Sleep(30 * time.Millisecond)
	incident, err := store.Publish(t.Context(), collaboration.PublishRequest{
		SenderID: recovered.ID, IdempotencyKey: "wait-incident", RecipientIDs: []string{parent.ID},
		Origin: collaboration.OriginRuntime, Kind: collaboration.EventPeerNote, Body: "Needs attention.",
		References: []collaboration.Reference{{Kind: "recovery_incident", ID: "incident-1", Revision: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.Status != "needs_attention" || got.Event == nil || got.Event.ID != incident.Event.ID {
			t.Fatalf("needs-attention observation=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("needs-attention event did not interrupt wait")
	}
}
