package session

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	recoverytypes "crude/types/recovery"
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

func TestPrepareInputAdmissionRejectsClosingSession(t *testing.T) {
	s := &Session{orchestrators: make(map[string]*Orchestrator)}
	o := NewOrchestrator("root", s)
	s.orchestrators[o.ID] = o
	atomic.StoreInt32(&s.closing, 1)
	if _, err := s.PrepareInputAdmission(o.ID, provider.PlainInput("unsubmitted draft"), 1); err == nil || o.TurnActive() || len(s.pendingAdmissions) != 0 {
		t.Fatal("closing session reserved or acknowledged new execution")
	}
}

func TestCloseCommitsPreparedInputWithoutEnqueuingExecution(t *testing.T) {
	s, err := NewWithSessionDir("close-admission", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	o, err := s.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	intent, err := s.PrepareInputAdmission(o.ID, provider.PlainInput("saved during shutdown"), 2)
	if err != nil {
		t.Fatal(err)
	}
	path := s.dbPath
	s.Close()
	if !intent.committed || intent.result.Status != InputAdmissionPreservationPending || len(o.inputCh) != 0 {
		t.Fatalf("shutdown launched or lost prepared input: %+v queued=%d", intent.result, len(o.inputCh))
	}
	db, err := sessiondb.OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	current, found, err := sessiondb.LoadContextState(db, s.sessionID, s.sessionID)
	if err != nil || !found || len(current.Messages) != 2 || current.Messages[0].InternalID != intent.InputID || current.Messages[1].InternalID != intent.InputID+"-context" || current.Messages[1].EffectiveVisibility() != provider.VisibilityContextOnly {
		t.Fatalf("shutdown discarded accepted input: %+v found=%v error=%v", current, found, err)
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
	if err != nil || !found || len(state.Messages) != 2 || state.Messages[0].InternalID != intent.InputID || state.Messages[1].InternalID != intent.InputID+"-context" {
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
	if err != nil || !found || len(state.Messages) != 2 || state.Messages[0].InternalID != intent.InputID || state.Messages[1].InternalID != intent.InputID+"-context" || state.Revision != first.Receipt.ContextRevision {
		t.Fatalf("duplicate commit appended context twice: %+v found=%v err=%v", state, found, err)
	}
}

func TestAdmittedInputMirrorsItsOwningTurnBeforeCompletion(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", fmt.Sprintf("answer %d", attempt))
	}))
	t.Cleanup(server.Close)
	cfg := modelAttributionConfig(server.URL, "turn-mirror-model")
	cfg.Memory.Consolidation = "off"
	s, err := NewWithSessionDir("", cfg, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	store := newTestMemoryStore(t)
	s.SetMemoryStore(store)
	sender := newMockEventSender()
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	submit := func(text string) {
		t.Helper()
		intent, err := s.PrepareInputAdmission(o.ID, provider.PlainInput(text), 1)
		if err != nil {
			t.Fatal(err)
		}
		if result := s.CommitInputAdmission(t.Context(), intent); result.Status != InputAdmissionAccepted {
			t.Fatalf("input not admitted: %+v", result)
		}
	}
	submit("first request")
	waitTurnDone(t, sender, o.ID)
	turns, err := store.TurnsForSession(t.Context(), s.sessionID, 0, 10)
	if err != nil || len(turns) != 1 || turns[0].Turn != 1 || turns[0].UserText != "first request" || turns[0].AssistantText != "answer 1" {
		t.Fatalf("first completed input lost its mirror: %+v error=%v", turns, err)
	}
	if _, err := store.DB().Exec(`CREATE TRIGGER reject_turn_mirror BEFORE INSERT ON memory_history_turns WHEN NEW.turn=2 BEGIN SELECT RAISE(FAIL,'turn mirror unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	submit("second request")
	var incident recoverytypes.Status
	deadline := time.After(5 * time.Second)
	waiting := true
	for waiting {
		select {
		case event := <-sender.ch:
			if event.typ == "done" || event.typ == "error" {
				t.Fatalf("failed turn mirror was acknowledged: %+v", event)
			}
			if status, ok := event.data.(recoverytypes.Status); event.typ == "recovery" && ok && status.State == recoverytypes.NeedsAttention {
				incident, waiting = status, false
			}
		case <-deadline:
			t.Fatal("failed turn mirror did not park finalization")
		}
	}
	if _, err := store.DB().Exec(`DROP TRIGGER reject_turn_mirror`); err != nil {
		t.Fatal(err)
	}
	if s.RetryRecoveryFor(o.ID, incident.ID, incident.Generation) != nil {
		t.Fatal("exact finalization incident could not retry its mirror")
	}
	waitTurnDone(t, sender, o.ID)
	turns, err = store.TurnsForSession(t.Context(), s.sessionID, 0, 10)
	if err != nil || len(turns) != 2 || turns[1].Turn != 2 || turns[1].UserText != "second request" || turns[1].AssistantText != "answer 2" || calls.Load() != 2 {
		t.Fatalf("repair replayed output or lost the owning turn: turns=%+v requests=%d error=%v", turns, calls.Load(), err)
	}
	rows, err := sessiondb.LoadMessagesWithMeta(s.db)
	if err != nil || len(rows) != 4 || rows[0].Turn != 1 || rows[1].Turn != 1 || rows[2].Turn != 2 || rows[3].Turn != 2 || rows[0].Meta.TurnGeneration != 1 || rows[2].Meta.TurnGeneration != 2 {
		t.Fatalf("durable messages have different turn owners: %+v error=%v", rows, err)
	}
	meta, err := sessiondb.GetMeta(s.db)
	if err != nil || meta.FirstPrompt.String != "first request" || meta.LastPrompt.String != "second request" || meta.TurnCount != 2 {
		t.Fatalf("prompt metadata did not follow admission: %+v error=%v", meta, err)
	}
}
