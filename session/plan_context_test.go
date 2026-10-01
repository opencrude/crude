package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"crude/plan"
)

func TestHumanTurnReceivesCanonicalPlanAfterCompaction(t *testing.T) {
	s := newTestSessionForPlan(t)
	defer s.Close()
	ctx := context.Background()
	if text, err := s.activePlanTurnContext(ctx); err != nil || text != "" {
		t.Fatalf("no active plan should add no turn context: %q %v", text, err)
	}
	store, err := s.planStore()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Update(ctx, plan.UpdateRequest{
		PlanID: "current", Objective: "Repair the user's workflow", Status: plan.StatusActive,
		Steps: []plan.PlanStep{{ID: "inspect", Description: "Inspect compaction", Status: plan.StepCompleted},
			{ID: "verify", Description: "Run build and lint", Status: plan.StepInProgress}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Active context can be replaced by a checkpoint, but the plan is owned by
	// the session store. Read the next human turn from that same owner.
	text, err := s.activePlanTurnContext(ctx)
	if err != nil || !strings.Contains(text, "Saved session state") {
		t.Fatalf("missing canonical plan on human turn: %q %v", text, err)
	}
	start := strings.IndexByte(text, '\n') + 1
	end := strings.Index(text[start:], "\n</active_plan>")
	if end < 0 {
		t.Fatalf("bad plan context: %q", text)
	}
	var snapshot struct {
		ID       string          `json:"id"`
		Revision uint64          `json:"revision"`
		Steps    []plan.PlanStep `json:"steps"`
	}
	if err := json.Unmarshal([]byte(text[start:start+end]), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ID != "current" || snapshot.Revision != 1 || len(snapshot.Steps) != 2 || snapshot.Steps[1].ID != "verify" {
		t.Fatalf("stale or incomplete plan after compaction: %+v", snapshot)
	}
	_, _, err = store.Update(ctx, plan.UpdateRequest{PlanID: "current", Revision: 1, Status: plan.StatusCancelled})
	if err != nil {
		t.Fatal(err)
	}
	if text, err := s.activePlanTurnContext(ctx); err != nil || text != "" {
		t.Fatalf("closed plan leaked into new turn: %q %v", text, err)
	}
}
