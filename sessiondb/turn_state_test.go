package sessiondb

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLatestTurnStatesPreserveCompletedHumanAcrossOrchestratorIdentity(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	if err := BeginAgentTurn(db, "session", "old-orchestrator", 9, "human"); err != nil {
		t.Fatal(err)
	}
	if err := FinishAgentTurn(db, "session", "old-orchestrator", 9, TurnCompleted); err != nil {
		t.Fatal(err)
	}
	if err := BeginAgentTurn(db, "session", "new-orchestrator", 10, "subagent_wake"); err != nil {
		t.Fatal(err)
	}
	latest, human, err := LatestTurnStates(context.Background(), db, "session")
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.AgentID != "new-orchestrator" || latest.State != TurnRunning ||
		human == nil || human.AgentID != "old-orchestrator" || human.State != TurnCompleted {
		t.Fatalf("latest=%+v human=%+v", latest, human)
	}
}
