package plan

import (
	"context"
	"sync"
	"testing"
)

func TestStore_SkipAndReopenStep(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _, _ = s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})

	// Skip requires a reason.
	_, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: 1, Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepSkipped, Reason: "not needed"}},
	})
	if err != nil {
		t.Fatalf("skip: %v", err)
	}

	// Reopen directly: lifecycle transitions are declarative and need no reason.
	_, _, err = s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: 2, Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepInProgress}},
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, _ := s.Get(ctx, "p1")
	if got.Revision != 3 {
		t.Errorf("revision: expected 3, got %d", got.Revision)
	}
	if got.Steps[0].Status != StepInProgress {
		t.Errorf("status: expected in_progress, got %s", got.Steps[0].Status)
	}
}

func TestStore_RejectsSecondActivePlan(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "One", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Update(ctx, UpdateRequest{
		PlanID: "p2", Objective: "Two", Status: StatusActive,
		Steps: []PlanStep{{ID: "b", Description: "B", Status: StepPending}},
	})
	if err == nil {
		t.Fatal("expected second active plan rejection")
	}
}

func TestStore_ConcurrentRevisionCAS(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, objective := range []string{"O2", "O3"} {
		objective := objective
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, updateErr := s.Update(ctx, UpdateRequest{
				PlanID: "p1", Revision: 1, Objective: objective, Status: StatusActive,
				Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
			})
			errs <- updateErr
		}()
	}
	wg.Wait()
	close(errs)
	success, conflicts := 0, 0
	for updateErr := range errs {
		if updateErr == nil {
			success++
			continue
		}
		if _, ok := updateErr.(*RevisionConflictError); ok {
			conflicts++
		} else {
			t.Fatalf("unexpected update error: %v", updateErr)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
	got, err := s.Get(ctx, "p1")
	if err != nil || got.Revision != 2 {
		t.Fatalf("final plan = %+v err=%v", got, err)
	}
}

func TestStore_EventsRetainCanonicalSnapshots(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _, _ = s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})
	_, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: 1, Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepCompleted}},
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Payload.Revision != 1 || events[1].Payload.Revision != 2 {
		t.Fatalf("events = %+v", events)
	}
}

func TestStore_UpdatePreservesOmittedTopLevelFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "Original objective", Status: StatusPaused,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: created.Revision, Explanation: "metadata-only update",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Objective != created.Objective || updated.Status != created.Status || len(updated.Steps) != 1 || updated.Steps[0].ID != "a" {
		t.Fatalf("omitted fields were not preserved: %+v", updated)
	}
}

func TestStore_PauseResumeCancelPlan(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _, _ = s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})

	_, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: 1, Objective: "O", Status: StatusPaused,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	// A paused plan is non-terminal, so it remains the session's active plan
	// until resumed or completed/cancelled.
	active, _ := s.Active(ctx, "sess-1")
	if active == nil || active.Status != StatusPaused {
		t.Errorf("expected paused plan to be active, got %+v", active)
	}

	cancelled, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: 2, Objective: "O", Status: StatusCancelled, Explanation: "abandoned",
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Steps[0].Status != StepCancelled || cancelled.Steps[0].Reason != "abandoned" {
		t.Fatalf("cancelled plan did not settle remaining work: %+v", cancelled.Steps)
	}
	// A cancelled plan may be reopened directly when reality changes.
	reopened, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: 3, Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepInProgress}},
	})
	if err != nil || reopened.Status != StatusActive || reopened.Steps[0].Status != StepInProgress {
		t.Fatalf("reopen cancelled plan = %+v err=%v", reopened, err)
	}
}

func TestStoreAcceptsOutOfOrderDependencyStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{
			{ID: "a", Description: "A", Status: StepPending},
			{ID: "b", Description: "B", Status: StepInProgress, DependsOn: []string{"a"}},
		},
	})
	if err != nil || created.Steps[1].Status != StepInProgress {
		t.Fatalf("out-of-order status = %+v err=%v", created, err)
	}
}

func TestStoreCancellationPreservesSettledExceptionsAndCancelsLiveWork(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{
			{ID: "done", Description: "Done", Status: StepCompleted},
			{ID: "skip", Description: "Skip", Status: StepSkipped, Reason: "not needed"},
			{ID: "fail", Description: "Fail", Status: StepFailed, Reason: "boom"},
			{ID: "block", Description: "Block", Status: StepBlocked, Reason: "waiting"},
			{ID: "live", Description: "Live", Status: StepInProgress},
			{ID: "todo", Description: "Todo", Status: StepPending},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: created.Revision, Status: StatusCancelled, Explanation: "deferred",
	})
	if err != nil {
		t.Fatal(err)
	}
	statuses := make(map[string]StepStatus, len(cancelled.Steps))
	for _, step := range cancelled.Steps {
		statuses[step.ID] = step.Status
	}
	for id, want := range map[string]StepStatus{
		"done": StepCompleted, "skip": StepSkipped, "fail": StepFailed, "block": StepBlocked,
		"live": StepCancelled, "todo": StepCancelled,
	} {
		if statuses[id] != want {
			t.Fatalf("status[%s]=%s want %s; plan=%+v", id, statuses[id], want, cancelled)
		}
	}
}

func TestStoreCancellationEventAndHydrationSnapshotAgree(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepInProgress}, {ID: "b", Description: "B", Status: StepPending}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, _, err := s.Update(ctx, UpdateRequest{PlanID: "p1", Revision: 1, Status: StatusCancelled, Explanation: "deferred"})
	if err != nil {
		t.Fatal(err)
	}
	hydrated, err := s.Get(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Type != "plan_cancelled" {
		t.Fatalf("events = %+v", events)
	}
	if hydrated.Revision != cancelled.Revision || events[1].Payload.Revision != cancelled.Revision {
		t.Fatalf("revision mismatch cancelled=%d hydrated=%d event=%d", cancelled.Revision, hydrated.Revision, events[1].Payload.Revision)
	}
	for _, snapshot := range []Plan{cancelled, hydrated, events[1].Payload} {
		for _, step := range snapshot.Steps {
			if step.Status != StepCancelled || step.Reason != "deferred" || step.FinishedAt == nil {
				t.Fatalf("snapshot lost cancellation lifecycle: %+v", snapshot)
			}
		}
	}
}

func TestStore_DuplicateStepIDRejected(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.Update(context.Background(), UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{
			{ID: "a", Description: "A", Status: StepPending},
			{ID: "a", Description: "A dup", Status: StepPending},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate id error")
	}
}

func TestStore_CompleteThenReopenPlan(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _, _ = s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})
	_, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: 1, Objective: "O", Status: StatusCompleted,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepCompleted}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	active, _ := s.Active(ctx, "sess-1")
	if active != nil {
		t.Errorf("completed plan should not be active")
	}
	reopened, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: 2, Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{
			ID: "a", Description: "A", Status: StepInProgress,
		}},
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.Revision != 3 || reopened.Status != StatusActive || reopened.Steps[0].Attempts != 1 {
		t.Fatalf("reopened plan = %+v", reopened)
	}
	active, err = s.Active(ctx, "sess-1")
	if err != nil || active == nil || active.ID != "p1" {
		t.Fatalf("active after reopen = %+v err=%v", active, err)
	}
}
