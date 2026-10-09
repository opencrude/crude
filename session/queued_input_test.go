package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
)

func TestQueuedInputSurvivesCloseAndPassiveResume(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"queued answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	cfg := modelAttributionConfig(server.URL, "queue-model")
	s, err := NewWithSessionDir("queue-resume", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	o, err := s.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	// Queue-only sessions are still saved work. Register a pending queued intent
	// and let the same shutdown barrier used by the product preserve it.
	s.compactionMu.Lock()
	s.compactionCancel, s.compactionAgent, s.compactionID = func() {}, o.ID, "queue-only"
	s.compactionStarted = true
	s.compactionMu.Unlock()
	intent, err := s.PrepareInputAdmission(o.ID, provider.PlainInput("survive without transcript"), 4)
	if err != nil {
		t.Fatal(err)
	}
	s.compactionMu.Lock()
	s.compactionCancel, s.compactionAgent, s.compactionID, s.compactionStarted = nil, "", "", false
	s.compactionMu.Unlock()
	path := s.dbPath
	s.Close()
	if intent.result.Status != InputAdmissionQueued || calls.Load() != 0 {
		t.Fatalf("close lost or executed queued input: %+v calls=%d", intent.result, calls.Load())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("queue-only session was pruned: %v", err)
	}
	resumed, err := ResumeSessionWithDir(path, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Close)
	queue := resumed.QueuedInputs()
	if len(queue) != 1 || queue[0].Input.InternalID != intent.InputID || calls.Load() != 0 {
		t.Fatalf("resume lost ownership or started work: %+v calls=%d", queue, calls.Load())
	}
}

func TestQueuedInputDequeueFailureRollsBackContextAndExecution(t *testing.T) {
	s, err := NewWithSessionDir("queue-rollback", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	sender := newMockEventSender()
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	s.compactionMu.Lock()
	s.compactionCancel, s.compactionAgent = func() {}, o.ID
	s.compactionStarted = true
	s.compactionMu.Unlock()
	input, err := s.PrepareInputAdmission(o.ID, provider.PlainInput("must remain pending"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if result := s.CommitInputAdmission(t.Context(), input); result.Status != InputAdmissionQueued {
		t.Fatalf("queue failed: %+v", result)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_queue_dequeue BEFORE UPDATE ON session_runtime_state
		WHEN NEW.key='queued_inputs' BEGIN SELECT RAISE(FAIL,'dequeue unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	s.compactionMu.Lock()
	s.compactionCancel, s.compactionAgent, s.compactionStarted = nil, "", false
	s.compactionMu.Unlock()
	s.AdmitQueuedInputs(o.ID)
	if queue := s.QueuedInputs(); len(queue) != 1 || queue[0].Input.InternalID != input.InputID || o.TurnActive() || s.HasMessages() {
		t.Fatalf("failed dequeue partially admitted input: queue=%+v active=%v messages=%v", queue, o.TurnActive(), s.HasMessages())
	}
	if _, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID); err != nil || found {
		t.Fatalf("dequeue failure committed context: found=%v err=%v", found, err)
	}
	for len(sender.ch) > 0 {
		if event := <-sender.ch; event.typ == "queued_input_admitted" || event.typ == "turn_started" {
			t.Fatalf("failed dequeue announced execution: %+v", event)
		}
	}
}

func TestQueuedInputResumeRejectsCorruption(t *testing.T) {
	s, err := NewWithSessionDir("queue-corruption", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := sessiondb.SaveRuntimeState(t.Context(), s.db, queuedInputsStateKey, []byte("not json")); err != nil {
		t.Fatal(err)
	}
	if err := s.loadQueuedInputs(); err == nil {
		t.Fatal("corrupt queue was silently treated as empty")
	}
}

func waitEvent(t *testing.T, sender *mockEventSender, typ string) agentEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-sender.ch:
			if event.typ == typ {
				return event
			}
		case <-deadline:
			t.Fatalf("event %q was not sent", typ)
		}
	}
}

// Input sent while the owner compacts is durably queued, announced as a
// pending row, and admitted in order once compaction finishes.
func TestInputDuringCompactionIsQueuedDurablyAndAdmittedAfter(t *testing.T) {
	s, err := NewWithSessionDir("queued-input", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	sender := newMockEventSender()
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	s.compactionMu.Lock()
	s.compactionCancel, s.compactionAgent, s.compactionID = func() {}, o.ID, "compact-test"
	s.compactionMu.Unlock()

	first, err := s.PrepareInputAdmission(o.ID, provider.PlainInput("first while compacting"), 4)
	if err != nil {
		t.Fatal(err)
	}
	result := s.CommitInputAdmission(context.Background(), first)
	if result.Status != InputAdmissionQueued || result.Receipt.InputID != first.InputID || result.Receipt.DraftRevision != 4 || result.Receipt.AcceptedAt.IsZero() {
		t.Fatalf("compacting owner did not queue input: %+v", result)
	}
	second, err := s.PrepareInputAdmission(o.ID, provider.PlainInput("second while compacting"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.CommitInputAdmission(context.Background(), second); got.Status != InputAdmissionQueued {
		t.Fatalf("second input not queued: %+v", got)
	}
	if event := waitEvent(t, sender, "input_queued"); event.agentID != o.ID {
		t.Fatalf("queued row announced for %q", event.agentID)
	}
	raw, err := sessiondb.LoadRuntimeState(context.Background(), s.DB(), queuedInputsStateKey)
	var persisted []QueuedInput
	if err != nil || json.Unmarshal(raw, &persisted) != nil || len(persisted) != 2 || persisted[0].Input.InternalID != first.InputID {
		t.Fatalf("queue is not durable before acknowledgement: %s err=%v", raw, err)
	}

	// Compaction ends: only the head is admitted (the second waits for the
	// first turn to complete naturally).
	s.compactionMu.Lock()
	s.compactionCancel, s.compactionAgent, s.compactionID = nil, "", ""
	s.compactionMu.Unlock()
	s.AdmitQueuedInputs(o.ID)
	if event := waitEvent(t, sender, "queued_input_admitted"); event.data.(map[string]any)["input_id"] != first.InputID {
		t.Fatalf("wrong input admitted: %+v", event.data)
	}
	state, found, err := sessiondb.LoadContextState(s.DB(), s.SessionID(), s.SessionID())
	if err != nil || !found || len(state.Messages) == 0 || state.Messages[0].InternalID != first.InputID {
		t.Fatalf("queued head was not admitted to context: %+v err=%v", state, err)
	}
	if queue := s.QueuedInputs(); len(queue) != 1 || queue[0].Input.InternalID != second.InputID {
		t.Fatalf("queue after head admission: %+v", queue)
	}
}
