package statemachine

import (
	"testing"

	"crude/logger"
)

func TestConversationSnapshotRestoresPlanAndBlocker(t *testing.T) {
	lg, err := logger.New(logger.Options{HomeDir: t.TempDir(), SessionID: "snapshot"})
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}
	defer lg.Close()
	machine := NewConversationStateMachine(lg)
	goal := machine.CreateGoal("session", "plan: fix lifecycle", "fix lifecycle", PriorityNormal, false)
	state := machine.GetOrCreateState("session")
	state.AddToContext("plan_goal_id", goal.ID)
	state.AddToContext("plan_step_statuses", map[string]string{"audit": "doing"})
	state.AddToContext("plan_step_labels", map[string]string{"audit": "Audit lifecycle"})
	state.AddToContext("plan_steps", []map[string]any{{"id": "audit", "description": "Audit lifecycle"}})
	machine.PauseWithBlocker("session", ConfirmationNeeded("continue?"))

	data, err := machine.Snapshot("session")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	restored := NewConversationStateMachine(lg)
	if err := restored.Restore("session", data); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := restored.GetActiveGoal("session"); got == nil || got.ID != goal.ID ||
		got.CurrentBlocker == nil || got.CurrentBlocker.Message != "continue?" {
		t.Fatalf("restored goal = %+v", got)
	}
	restoredState := restored.GetOrCreateState("session")
	statuses, ok := restoredState.ContextData["plan_step_statuses"].(map[string]string)
	if !ok || statuses["audit"] != "doing" {
		t.Fatalf("restored statuses = %#v", restoredState.ContextData["plan_step_statuses"])
	}
	if _, ok := restoredState.ContextData["plan_steps"].([]map[string]any); !ok {
		t.Fatalf("restored steps type = %T", restoredState.ContextData["plan_steps"])
	}
}
