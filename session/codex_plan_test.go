package session

import (
	"encoding/json"
	"testing"

	"crude/plan"
)

func TestNativePlanUpdatesUseCanonicalStore(t *testing.T) {
	s := newTestSessionForPlan(t)
	defer s.Close()
	params := func(steps ...map[string]any) map[string]any {
		items := make([]any, len(steps))
		for i, s := range steps {
			items[i] = s
		}
		return map[string]any{"plan": items}
	}
	result, err := s.executePlanTool(t.Context(), "update_plan", params(map[string]any{"step": "Inspect flow", "status": "in_progress"}, map[string]any{"step": "Old task", "status": "pending"}))
	if err != nil {
		t.Fatal(err)
	}
	var first plan.Plan
	if err := json.Unmarshal([]byte(result.Content), &first); err != nil {
		t.Fatal(err)
	}
	result, err = s.executePlanTool(t.Context(), "update_plan", params(map[string]any{"step": "Inspect flow", "status": "completed"}, map[string]any{"step": "Ship change", "status": "completed"}))
	if err != nil {
		t.Fatal(err)
	}
	var last plan.Plan
	if err := json.Unmarshal([]byte(result.Content), &last); err != nil {
		t.Fatal(err)
	}
	if last.ID != first.ID || last.Revision != 2 || last.Status != plan.StatusCompleted || last.Steps[0].ID != first.Steps[0].ID || last.Steps[2].Status != plan.StepCancelled {
		t.Fatalf("plan %+v", last)
	}
}
