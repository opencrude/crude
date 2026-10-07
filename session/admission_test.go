package session

import (
	"context"
	"path/filepath"
	"testing"

	"crude/provider"
	"crude/sessiondb"
)

func TestAdmitInputRejectsUnavailableOwnerWithoutPanic(t *testing.T) {
	var nilSession *Session
	if _, err := nilSession.PrepareInputAdmission("missing", provider.PlainInput("draft"), 1); err == nil {
		t.Fatal("nil session prepared an admission")
	}
	s := &Session{orchestrators: map[string]*Orchestrator{}}
	if _, err := s.PrepareInputAdmission("missing", provider.PlainInput("draft"), 1); err == nil {
		t.Fatal("missing owner prepared an admission")
	}
}

func TestReserveTurnFencesStopBeforeQueue(t *testing.T) {
	o := NewOrchestrator("root", nil)
	request, ok := o.reserveTurn(provider.PlainInput("accepted"), nil, nil)
	if !ok {
		t.Fatal("reservation failed")
	}
	if stopped, _ := o.CancelTurn(); !stopped {
		t.Fatal("stop did not reach reserved generation")
	}
	if o.enqueueReserved(request) {
		t.Fatal("cancelled reservation was queued for execution")
	}
}

func TestCommitInputAdmissionAfterStopPersistsWithoutStartingGeneration(t *testing.T) {
	s, err := NewWithSessionDir("admission-stop", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	o, err := s.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	intent, err := s.PrepareInputAdmission(o.ID, provider.PlainInput("persist after Stop"), 9)
	if err != nil {
		t.Fatal(err)
	}
	if stopped, _ := o.CancelTurn(); !stopped {
		t.Fatal("Stop did not fence prepared generation")
	}
	result := s.CommitInputAdmission(context.Background(), intent)
	if result.Status != InputAdmissionPreservationPending || result.Receipt.InputID != intent.InputID || result.Receipt.DraftRevision != 9 {
		t.Fatalf("stopped commit result=%+v", result)
	}
	state, found, err := sessiondb.LoadContextState(s.DB(), s.SessionID(), s.SessionID())
	if err != nil || !found || len(state.Messages) != 1 || state.Messages[0].InternalID != intent.InputID {
		t.Fatalf("accepted input missing after Stop: %+v found=%v err=%v", state, found, err)
	}
}

func TestCommitInputAdmissionDuplicateReturnsSameReceipt(t *testing.T) {
	s, err := NewWithSessionDir("admission-idempotent", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	o, err := s.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	intent, err := s.PrepareInputAdmission(o.ID, provider.PlainInput("commit once"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if stopped, _ := o.CancelTurn(); !stopped {
		t.Fatal("failed to stop prepared generation")
	}
	first := s.CommitInputAdmission(context.Background(), intent)
	second := s.CommitInputAdmission(context.Background(), intent)
	if first.Status != InputAdmissionPreservationPending || second.Status != first.Status || second.Receipt != first.Receipt {
		t.Fatalf("duplicate commit changed receipt: first=%+v second=%+v", first, second)
	}
	state, found, err := sessiondb.LoadContextState(s.DB(), s.SessionID(), s.SessionID())
	if err != nil || !found || len(state.Messages) != 1 {
		t.Fatalf("duplicate commit appended context twice: %+v found=%v err=%v", state, found, err)
	}
}
