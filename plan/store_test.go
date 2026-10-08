package plan

import (
	"context"
	"reflect"
	"testing"

	"crude/sessiondb"
)

func TestStoreCleanSnapshotsAndAtomicReplacement(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	p, _, err := s.Update(ctx, UpdateRequest{PlanID: "p", Objective: "Old goal", Steps: []PlanStep{{ID: "keep", Description: "Keep", Status: StepInProgress}, {ID: "obsolete", Description: "Obsolete", Status: StepPending}}})
	if err != nil {
		t.Fatal(err)
	}
	p, _, err = s.Update(ctx, UpdateRequest{PlanID: p.ID, Revision: p.Revision, Steps: []PlanStep{{ID: "keep", Description: "Keep", Status: StepCompleted}}})
	if err != nil || len(p.Steps) != 1 {
		t.Fatalf("removal: %+v %v", p, err)
	}
	prior := s.ActivitySnapshot()
	for _, req := range []UpdateRequest{
		{PlanID: p.ID, Revision: 1, Replace: true, Objective: "New goal", Steps: []PlanStep{{ID: "new", Description: "New", Status: StepPending}}},
		{PlanID: p.ID, Revision: p.Revision, Replace: true},
		{PlanID: p.ID, Revision: p.Revision, Replace: true, Objective: "New goal", Steps: []PlanStep{{ID: "new", Description: "New", Status: StepPending, DependsOn: []string{"obsolete"}}}},
	} {
		if _, _, err := s.Update(ctx, req); err == nil {
			t.Fatal("invalid/stale replacement succeeded")
		}
		got, err := s.Get(ctx, p.ID)
		if err != nil || !reflect.DeepEqual(got, p) || s.ActivitySnapshot() != prior {
			t.Fatalf("failed replacement mutated state: %+v %v", got, err)
		}
	}
	// Fail after row and step writes, proving the whole replacement rolls back.
	if _, err := s.DB().Exec(`CREATE TRIGGER fail_replace BEFORE INSERT ON plan_events WHEN NEW.event_type = 'plan_replaced' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	req := UpdateRequest{PlanID: p.ID, Revision: p.Revision, Replace: true, Objective: "New goal", Steps: []PlanStep{{ID: "keep", Description: "New work", Status: StepPending}}}
	if _, _, err := s.Update(ctx, req); err == nil {
		t.Fatal("injected transaction failure succeeded")
	}
	got, err := s.Get(ctx, p.ID)
	if err != nil || !reflect.DeepEqual(got, p) || s.ActivitySnapshot() != prior {
		t.Fatal("transaction failure changed current state")
	}
	if _, err := s.DB().Exec(`DROP TRIGGER fail_replace`); err != nil {
		t.Fatal(err)
	}
	p, _, err = s.Update(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if p.Objective != "New goal" || len(p.Steps) != 1 || p.Steps[0].Attempts != 0 || p.Steps[0].StartedAt != nil || p.Steps[0].FinishedAt != nil {
		t.Fatalf("replacement: %+v", p)
	}
	events, err := s.Events(ctx, p.ID)
	if err != nil || len(events) != 3 || len(events[0].Payload.Steps) != 2 || events[2].Type != "plan_replaced" {
		t.Fatalf("history: %+v %v", events, err)
	}
	active, err := s.Active(ctx, "sess-1")
	if err != nil || !reflect.DeepEqual(*active, p) || s.ActivitySnapshot().Objective != "New goal" || s.ActivitySnapshot().Total != 1 {
		t.Fatal("active projection retained old goal")
	}
	resumed := NewStore(s.DB(), "sess-1")
	if err := resumed.RestoreActivity(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.ActivitySnapshot(), resumed.ActivitySnapshot()) {
		t.Fatal("resume changed replacement")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := sessiondb.OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := sessiondb.CreateSessionRow(db, "sess-1"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return NewStore(db, "sess-1")
}

func TestStore_CreateRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, cs, err := s.Update(ctx, UpdateRequest{
		PlanID:    "plan-123",
		Objective: "Fix persistence",
		Status:    StatusActive,
		Steps: []PlanStep{
			{ID: "audit", Description: "Audit", Status: StepPending},
			{ID: "verify", Description: "Verify", Status: StepPending, DependsOn: []string{"audit"}},
		},
	})
	if err != nil {
		t.Fatalf("Update create: %v", err)
	}
	if cs.Revision != 1 {
		t.Errorf("revision: expected 1, got %d", cs.Revision)
	}
	if created.ID != "plan-123" {
		t.Errorf("id: expected plan-123, got %s", created.ID)
	}

	got, err := s.Get(ctx, "plan-123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusActive || got.Objective != "Fix persistence" {
		t.Errorf("got %+v", got)
	}
	if len(got.Steps) != 2 || got.Steps[1].DependsOn[0] != "audit" {
		t.Errorf("steps: %+v", got.Steps)
	}
}

func TestStore_UpdateIncrementsRevision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})
	if err != nil {
		t.Fatal(err)
	}

	plan2, cs, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: 1, Objective: "O2", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepCompleted}},
	})
	if err != nil {
		t.Fatalf("Update existing: %v", err)
	}
	if cs.Revision != 2 {
		t.Errorf("revision: expected 2, got %d", cs.Revision)
	}
	if plan2.Objective != "O2" {
		t.Errorf("objective: expected O2, got %s", plan2.Objective)
	}
	if plan2.Steps[0].Status != StepCompleted {
		t.Errorf("step status: expected completed, got %s", plan2.Steps[0].Status)
	}
}

func TestStore_StaleRevisionConflict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _, _ = s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})

	_, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: 99, Objective: "O3", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})
	if err == nil {
		t.Fatal("expected stale revision conflict")
	}
}

func TestStore_InvalidUpdateRollsBack(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _, _ = s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})

	// A cycle must fail and leave revision 1 intact.
	_, _, err := s.Update(ctx, UpdateRequest{
		PlanID: "p1", Revision: 1, Objective: "O", Status: StatusActive,
		Steps: []PlanStep{
			{ID: "a", Description: "A", Status: StepPending, DependsOn: []string{"b"}},
			{ID: "b", Description: "B", Status: StepPending, DependsOn: []string{"a"}},
		},
	})
	if err == nil {
		t.Fatal("expected cycle error")
	}

	got, _ := s.Get(ctx, "p1")
	if got.Revision != 1 {
		t.Errorf("revision after rollback: expected 1, got %d", got.Revision)
	}
	if len(got.Steps) != 1 {
		t.Errorf("steps after rollback: expected 1, got %d", len(got.Steps))
	}
}

func TestStore_ActiveAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _, _ = s.Update(ctx, UpdateRequest{
		PlanID: "p1", Objective: "O", Status: StatusActive,
		Steps: []PlanStep{{ID: "a", Description: "A", Status: StepPending}},
	})

	active, err := s.Active(ctx, "sess-1")
	if err != nil || active == nil {
		t.Fatalf("Active: %v %v", active, err)
	}
	if active.ID != "p1" {
		t.Errorf("active id: expected p1, got %s", active.ID)
	}

	sums, err := s.List(ctx, "sess-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sums) != 1 || sums[0].ID != "p1" {
		t.Errorf("list: %+v", sums)
	}
}

func TestStore_CompletedPlanIsNotActive(t *testing.T) {
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
		t.Fatal(err)
	}
	active, _ := s.Active(ctx, "sess-1")
	if active != nil {
		t.Errorf("expected no active plan, got %+v", active)
	}
}

func TestStore_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Get(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected not found error")
	}
}
