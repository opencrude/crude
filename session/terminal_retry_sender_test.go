package session

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	recoverytypes "crude/types/recovery"
)

func TestTerminalRetryUsesPublishedOwnerBeforeRunLoopStarts(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sessiondb.CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	input := provider.NewTextMessage("user", "retain accepted input")
	input.InternalID = "accepted"
	state, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: "session", AgentID: "session", Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "initial", Messages: []provider.ChatMessage{input}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.BeginAgentTurn(db, "session", "root", 1, "human"); err != nil {
		t.Fatal(err)
	}
	sender := newMockEventSender()
	o := &Orchestrator{ID: "root", sender: sender, stopCh: make(chan struct{})}
	s := &Session{db: db, sessionID: "session", orchestrators: map[string]*Orchestrator{"root": o},
		contextRevision: state.Revision, contextGeneration: state.Generation, contextReason: state.Reason, activeContext: state.Messages}
	// No run loop is started, so the asynchronous session-wide sender remains
	// nil. The published orchestrator already owns completion delivery.
	intent, err := json.Marshal(terminalSettlementIntent{SessionID: "session", AgentID: "root", OwnerID: "session", Generation: 1, TurnState: sessiondb.TurnCompleted})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := sessiondb.RecoveryCheckpoint{Status: recoverytypes.Status{ID: "retry", AgentID: "root", Generation: 1, State: recoverytypes.NeedsAttention, StartedAt: time.Now()}, ContextOwnerID: "session", ContextRevision: state.Revision, ContextGeneration: state.Generation, ContextReason: state.Reason, ActiveContext: state.Messages, TerminalSettlement: intent}
	if !s.retryTerminalSettlement(checkpoint) {
		t.Fatal("exact terminal retry rejected")
	}
	done := make(chan struct{})
	go func() { s.orchWg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		close(o.stopCh)
		<-done
		t.Fatal("terminal settlement did not finish")
	}
	select {
	case event := <-sender.ch:
		for event.typ != "done" {
			select {
			case event = <-sender.ch:
			default:
				t.Fatal("completed retry lost its owner notification")
			}
		}
		if event.agentID != "root" {
			t.Fatalf("foreign completion: %+v", event)
		}
	default:
		t.Fatal("completed retry lost its owner notification before run loop startup")
	}
}
