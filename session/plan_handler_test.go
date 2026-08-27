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
