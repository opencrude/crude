package plan

import (
	"context"
	"reflect"
	"testing"
)

func TestActivitySnapshotTracksCommittedPlanAndResume(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p, _, err := s.Update(ctx, UpdateRequest{PlanID: "plan", Objective: "Fix activity", Status: StatusActive, Steps: []PlanStep{{ID: "one", Description: "Inspect", Status: StepCompleted}, {ID: "two", Description: "Implement", Status: StepInProgress}, {ID: "three", Description: "Verify", Status: StepPending}}})
	if err != nil {
		t.Fatal(err)
	}
	first := s.ActivitySnapshot()
	if first == nil || first.Completed != 1 || len(first.Current) != 1 || first.Current[0].Number != 2 || first.Next.Number != 3 {
		t.Fatalf("snapshot=%+v", first)
	}
	_, _, err = s.Update(ctx, UpdateRequest{PlanID: p.ID, Revision: p.Revision + 1, Status: StatusPaused})
	if err == nil || s.ActivitySnapshot() != first {
		t.Fatal("failed revision changed activity projection")
	}
	p, _, err = s.Update(ctx, UpdateRequest{PlanID: p.ID, Revision: p.Revision, Status: StatusPaused})
	if err != nil {
		t.Fatal(err)
	}
	if s.ActivitySnapshot().Status != "paused" || first.Status != "active" {
		t.Fatal("snapshot mutated after publication")
	}
	resumed := NewStore(s.DB(), "sess-1")
	if err := resumed.RestoreActivity(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.ActivitySnapshot(), resumed.ActivitySnapshot()) {
		t.Fatal("resume lost active plan progress")
	}
	_, _, err = s.Update(ctx, UpdateRequest{PlanID: p.ID, Revision: p.Revision, Status: StatusCancelled, Explanation: "stopped"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ActivitySnapshot() != nil {
		t.Fatal("closed plan remained in activity")
	}
	if err := s.DB().Close(); err != nil {
		t.Fatal(err)
	}
	if resumed.ActivitySnapshot() == nil {
		t.Fatal("reading a retained projection depended on an open database")
	}
}
