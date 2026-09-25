package session

import (
	"testing"

	"crude/provider"
	"crude/sessiondb"
)

func TestResumeTurnGenerationIncludesUnbilledCancelledTurns(t *testing.T) {
	db := sessiondb.OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := sessiondb.CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.BeginAgentTurn(db, "session", "root", 9, "human"); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.FinishAgentTurn(db, "session", "root", 9, sessiondb.TurnInterrupted); err != nil {
		t.Fatal(err)
	}
	turns, err := sessiondb.AgentTurnGenerations(db, "session")
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{recoveredTurnGenerations: turns, orchestrators: make(map[string]*Orchestrator)}
	o := NewOrchestrator("root", s)
	s.orchestrators[o.ID] = o
	if got := s.AgentTurnGeneration(o.ID); got != 9 {
		t.Fatalf("resume rewound unbilled turn to %d", got)
	}
	if !o.SubmitInput(provider.UserInput{Text: "continue"}) {
		t.Fatal("input not admitted")
	}
	defer o.CancelTurn()
	if got := s.AgentTurnGeneration(o.ID); got != 10 {
		t.Fatalf("reused cancelled generation: %d", got)
	}
}
