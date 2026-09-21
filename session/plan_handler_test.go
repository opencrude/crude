package session

import (
	"context"
	"encoding/json"
	"testing"

	"crude/plan"
	"crude/sessiondb"
)

func newTestSessionForPlan(t *testing.T) *Session {
	t.Helper()
	sm := NewSessionManager()
	info, err := sm.NewSession(testConfig(), t.TempDir())
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	sender := newMockEventSender()
	info.Session.sender = sender
	return info.Session
}

func TestExecutePlanTool_UpdateReturnsCanonicalPlan(t *testing.T) {
	s := newTestSessionForPlan(t)

	params := map[string]any{
		"plan_id":   "plan-123",
		"objective": "Fix persistence",
		"status":    "active",
		"steps": []any{
			map[string]any{"id": "audit", "description": "Audit flow", "status": "pending"},
			map[string]any{"id": "verify", "description": "Verify", "status": "pending", "depends_on": []any{"audit"}},
		},
	}
	result, err := s.executePlanTool(context.Background(), "update_plan", params)
	if err != nil {
		t.Fatalf("update_plan: %v", err)
	}
	var plan map[string]any
	if err := json.Unmarshal([]byte(result.Content), &plan); err != nil {
		t.Fatalf("result not JSON: %v", err)
	}
	if plan["id"] != "plan-123" {
		t.Errorf("plan id = %v", plan["id"])
	}

}

func TestExecutePlanTool_GetReturnsActive(t *testing.T) {
	s := newTestSessionForPlan(t)
	_, _ = s.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id": "plan-1", "objective": "O", "status": "active",
		"steps": []any{map[string]any{"id": "a", "description": "A", "status": "pending"}},
	})
	result, err := s.executePlanTool(context.Background(), "get_plan", map[string]any{})
	if err != nil {
		t.Fatalf("get_plan: %v", err)
	}
	if !containsString(result.Content, "plan-1") {
		t.Errorf("get_plan did not return active plan: %q", result.Content)
	}
}

func TestExecutePlanTool_RevisionConflictReturnsPlan(t *testing.T) {
	s := newTestSessionForPlan(t)
	_, _ = s.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id": "plan-1", "objective": "O", "status": "active",
		"steps": []any{map[string]any{"id": "a", "description": "A", "status": "pending"}},
	})
	// Revision 99 does not match current revision 1 -> structured tool error
	// carrying the canonical plan.
	result, err := s.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id": "plan-1", "revision": 99, "objective": "O2", "status": "active",
		"steps": []any{map[string]any{"id": "a", "description": "A", "status": "pending"}},
	})
	if err == nil {
		t.Fatal("expected revision conflict error")
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(result.Content), &response); err != nil {
		t.Fatalf("conflict response not JSON: %v", err)
	}
	if response["error"] != "revision_conflict" || response["current_plan"] == nil {
		t.Errorf("conflict response = %#v", response)
	}
}

func TestExecutePlanTool_UpdateMayOmitUnchangedObjectiveAndSteps(t *testing.T) {
	s := newTestSessionForPlan(t)
	_, err := s.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id": "plan-1", "objective": "Original", "status": "active",
		"steps": []any{map[string]any{"id": "a", "description": "A", "status": "pending"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id": "plan-1", "revision": 1, "objective": "",
		"explanation": "No structural change",
	})
	if err != nil {
		t.Fatalf("metadata-only update failed: %v, result=%s", err, result.Content)
	}
	var updated plan.Plan
	if err := json.Unmarshal([]byte(result.Content), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Objective != "Original" || len(updated.Steps) != 1 || updated.Status != plan.StatusActive {
		t.Fatalf("preserved update = %+v", updated)
	}
}

func TestExecutePlanTool_CreateStillRequiresObjectiveAndSteps(t *testing.T) {
	s := newTestSessionForPlan(t)
	result, err := s.executePlanTool(context.Background(), "update_plan", map[string]any{"plan_id": "plan-1"})
	if err == nil {
		t.Fatal("expected create validation error")
	}
	if !containsString(result.Content, "invalid_plan_update") {
		t.Fatalf("structured create error = %q", result.Content)
	}
}

// Models legitimately reach for other field spellings and shapes when calling
// update_plan. These repairs keep an obvious call from costing a whole turn,
// mirroring the alias/backfill layer other harnesses use for their task tools.
// Step annotations are the one thing never repaired: a result/note/outcome is
// dropped so the card stays a banner instead of becoming a progress log.
func TestExecutePlanTool_RepairsAliasedStepFields(t *testing.T) {
	s := newTestSessionForPlan(t)
	result, err := s.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id": "plan-1",
		"goal":    "Ship the fix",
		"steps": []any{
			map[string]any{"id": "audit", "content": "Audit repo", "state": "completed", "outcome": "found 3 bugs", "note": "extra narration"},
			map[string]any{"title": "Fix window bounds"},
			"Run gofmt + lint",
		},
	})
	if err != nil {
		t.Fatalf("aliased update rejected: %v, result=%s", err, result.Content)
	}
	var updated plan.Plan
	if err := json.Unmarshal([]byte(result.Content), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Objective != "Ship the fix" {
		t.Fatalf("objective = %q", updated.Objective)
	}
	if len(updated.Steps) != 3 {
		t.Fatalf("steps = %+v", updated.Steps)
	}
	if updated.Steps[0].Description != "Audit repo" {
		t.Fatalf("aliased step 0 = %+v (result annotations must be dropped)", updated.Steps[0])
	}
	if updated.Steps[1].ID != "fix-window-bounds" || updated.Steps[1].Description != "Fix window bounds" {
		t.Fatalf("derived id for step 1 = %+v", updated.Steps[1])
	}
	if updated.Steps[2].Description != "Run gofmt + lint" || updated.Steps[2].Status != plan.StepPending {
		t.Fatalf("string step = %+v", updated.Steps[2])
	}
}

func TestExecutePlanTool_RepairsStepsSentAsEncodedString(t *testing.T) {
	s := newTestSessionForPlan(t)
	result, err := s.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id":   "plan-1",
		"objective": "O",
		"todos":     `[{"id":"a","description":"Audit","status":"pending"}]`,
	})
	if err != nil {
		t.Fatalf("encoded steps rejected: %v, result=%s", err, result.Content)
	}
	var updated plan.Plan
	if err := json.Unmarshal([]byte(result.Content), &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Steps) != 1 || updated.Steps[0].ID != "a" {
		t.Fatalf("encoded steps = %+v", updated.Steps)
	}
}

func TestExecutePlanTool_SteersUnusableStepShapes(t *testing.T) {
	s := newTestSessionForPlan(t)
	cases := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{
			name:   "steps sent as a non-JSON string",
			params: map[string]any{"plan_id": "plan-1", "objective": "O", "steps": "audit then verify"},
			want:   "pass the array itself",
		},
		{
			name:   "step object without id or description",
			params: map[string]any{"plan_id": "plan-1", "objective": "O", "steps": []any{map[string]any{"notes": "do the thing"}}},
			want:   "every step needs a stable id and a short description",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.executePlanTool(context.Background(), "update_plan", c.params)
			if err == nil || !containsString(err.Error(), c.want) {
				t.Fatalf("error = %v, want %q", err, c.want)
			}
		})
	}
}

func TestExecutePlanToolAcceptsOutOfOrderCancellationSnapshot(t *testing.T) {
	s := newTestSessionForPlan(t)
	_, err := s.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id": "plan-1", "objective": "O", "status": "active",
		"steps": []any{
			map[string]any{"id": "trace", "description": "Trace", "status": "in_progress"},
			map[string]any{"id": "fix", "description": "Fix", "status": "pending", "depends_on": []any{"trace"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id": "plan-1", "revision": 1, "status": "cancelled", "explanation": "deferred",
		"steps": []any{
			map[string]any{"id": "trace", "description": "Trace", "status": "cancelled"},
			map[string]any{"id": "fix", "description": "Fix", "status": "pending", "depends_on": []any{"trace"}},
		},
	})
	if err != nil {
		t.Fatalf("cancellation snapshot rejected: %v, result=%s", err, result.Content)
	}
	var updated plan.Plan
	if err := json.Unmarshal([]byte(result.Content), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status != plan.StatusCancelled || len(updated.Steps) != 2 {
		t.Fatalf("cancelled plan = %+v", updated)
	}
	for _, step := range updated.Steps {
		if step.Status != plan.StepCancelled || step.FinishedAt == nil {
			t.Fatalf("cancelled plan left live step: %+v", step)
		}
	}
}

func TestExecutePlanToolReopensCancelledPlanWithoutExplanation(t *testing.T) {
	s := newTestSessionForPlan(t)
	_, err := s.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id": "plan-1", "objective": "O", "status": "cancelled",
		"steps": []any{map[string]any{"id": "a", "description": "A", "status": "cancelled"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id": "plan-1", "revision": 1, "status": "active",
		"steps": []any{map[string]any{"id": "a", "description": "A", "status": "in_progress"}},
	})
	if err != nil {
		t.Fatalf("reopen rejected: %v, result=%s", err, result.Content)
	}
	var updated plan.Plan
	if err := json.Unmarshal([]byte(result.Content), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status != plan.StatusActive || updated.Steps[0].Status != plan.StepInProgress {
		t.Fatalf("reopened plan = %+v", updated)
	}
}

func TestExecutePlanTool_GetHistory(t *testing.T) {
	s := newTestSessionForPlan(t)
	_, err := s.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id": "plan-1", "objective": "O", "status": "active",
		"steps": []any{map[string]any{"id": "a", "description": "A", "status": "pending"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.executePlanTool(context.Background(), "get_plan", map[string]any{
		"plan_id": "plan-1", "include_history": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Events []any `json:"events"`
	}
	if err := json.Unmarshal([]byte(result.Content), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 1 {
		t.Fatalf("history events = %#v", response.Events)
	}
}

func TestExecutePlanToolStoresAreSessionIsolated(t *testing.T) {
	main := newTestSessionForPlan(t)
	_, err := main.executePlanTool(context.Background(), "update_plan", map[string]any{
		"plan_id": "shared-id", "objective": "Main plan", "status": "active",
		"steps": []any{map[string]any{"id": "a", "description": "A", "status": "pending"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	subDB := sessiondb.OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = subDB.Close() })
	if err := sessiondb.CreateSessionRow(subDB, "sub-session"); err != nil {
		t.Fatal(err)
	}
	subStore := plan.NewStore(subDB, "sub-session")
	_, err = executePlanToolWithStore(context.Background(), subStore, "sub-session", "update_plan", map[string]any{
		"plan_id": "shared-id", "objective": "Sub-agent plan", "status": "active",
		"steps": []any{map[string]any{"id": "b", "description": "B", "status": "pending"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	mainPlan, err := main.planStore()
	if err != nil {
		t.Fatal(err)
	}
	gotMain, err := mainPlan.Get(context.Background(), "shared-id")
	if err != nil || gotMain.Objective != "Main plan" {
		t.Fatalf("main plan = %+v err=%v", gotMain, err)
	}
	gotSub, err := subStore.Get(context.Background(), "shared-id")
	if err != nil || gotSub.Objective != "Sub-agent plan" {
		t.Fatalf("sub plan = %+v err=%v", gotSub, err)
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) != 0 && indexOfSub(s, substr) >= 0)
}

func indexOfSub(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
