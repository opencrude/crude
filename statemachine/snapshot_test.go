package statemachine

import (
	"testing"

	"crude/logger"
)

func TestConversationSnapshotRestoresGoalBlockerAndContext(t *testing.T) {
	lg, err := logger.New(logger.Options{HomeDir: t.TempDir(), SessionID: "snapshot"})
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}
	defer lg.Close()
	machine := NewConversationStateMachine(lg)
	goal := machine.CreateGoal("session", "fix lifecycle", "fix lifecycle", PriorityNormal, false)
	state := machine.GetOrCreateState("session")
	state.AddToContext("workflow_state", "doing")
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
	if got := restoredState.ContextData["workflow_state"]; got != "doing" {
		t.Fatalf("restored context = %#v", got)
	}
}
