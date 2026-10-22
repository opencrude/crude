package session

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	recoverytypes "crude/types/recovery"
)

func TestPassiveRecoveryRestoresAcceptedTranscriptAndClosesUnknownToolExchange(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"continued safely\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	cfg := modelAttributionConfig(server.URL, "resume-recovery-journey")
	original, err := NewWithSessionDir("recovery-journey", cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	path, sessionID := original.dbPath, original.sessionID
	human := provider.NewTextMessage("user", "exact task")
	human.InternalID = "human-1"
	original.msgs = []provider.ChatMessage{human}
	original.msgTurns = []int{1}
	original.displayMetas = []sessiondb.DisplayMeta{canonicalizeDisplayMeta(human, sessiondb.DisplayMeta{DisplayRole: "user"}, "", 0, time.Now())}
	if _, err := original.publishRootContext(sessiondb.ContextPublication{Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{
		contextDelta(human, original.displayMetas[0], 1, original.displayMetas[0].Snapshots[0].Time, "old-root"),
	}}}); err != nil {
		t.Fatal(err)
	}
	accepted := provider.NewTextMessage("assistant", "accepted before process death")
	accepted.ToolCalls = []provider.ToolCall{{
		ID: "uncertain-call", Type: "function",
		Function: provider.FunctionCall{Name: "write_file", Arguments: `{"path":"owned.txt","content":"once"}`},
	}}
	archive := []provider.ChatMessage{human, accepted}
	checkpoint := sessiondb.RecoveryCheckpoint{
		Status:        recoverytypes.Status{ID: "crash-incident", AgentID: "old-root", Generation: 4, StartedAt: time.Now()},
		PendingInput:  provider.UserInput{InternalID: human.InternalID, Text: human.ContentString()},
		InputAdmitted: true, Archive: archive, ActiveContext: archive,
		ContextOwnerID: sessionID, ContextGeneration: 2, ContextReason: "accepted_crash_boundary",
	}
	state, err := sessiondb.PublishContext(original.db, sessiondb.ContextPublication{SessionID: sessionID, AgentID: sessionID, ExpectedRevision: original.contextRevision, Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 2, Reason: checkpoint.ContextReason, Messages: canonicalContextMessages(checkpoint.ActiveContext)}}})
	if err != nil {
		original.Close()
		t.Fatal(err)
	}
	checkpoint.ContextRevision = state.Revision
	if err := sessiondb.SaveRecoveryCheckpoint(original.db, sessionID, checkpoint); err != nil {
		original.Close()
		t.Fatal(err)
	}
	checkpoint, _, err = sessiondb.LoadRecoveryCheckpoint(original.db, sessionID, "old-root")
	if err != nil {
		original.Close()
		t.Fatal(err)
	}
	original.Close()
	resumed, err := ResumeSessionWithDir(path, cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	views := resumed.MessagesWithMeta()
	if len(views) < 2 || views[0].Msg.ContentString() != "exact task" ||
		views[1].Msg.ContentString() != accepted.ContentString() || len(views[1].Meta.Snapshots) == 0 {
		t.Fatalf("accepted checkpoint disappeared from resumed visible transcript: %+v", views)
	}
	active := resumed.activeContext
	if len(active) < 3 || active[2].ToolCallID != "uncertain-call" ||
		!active[2].ToolError || !strings.Contains(strings.ToLower(active[2].ContentString()), "unknown") ||
		strings.Contains(strings.ToLower(active[2].ContentString()), "cancelled by user") {
		t.Fatalf("crashed tool was not truthfully reconciled: %+v", active)
	}
	if requests.Load() != 0 {
		t.Fatal("passive resume contacted the provider")
	}
	sender := newMockEventSender()
	orch, err := resumed.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	status := resumed.pendingRecoveries[orch.ID].Status
	if resumed.RetryRecoveryFor(orch.ID, status.ID, status.Generation) != nil {
		t.Fatal("explicit continuation was rejected")
	}
	deadline := time.After(5 * time.Second)
	waiting := true
	for waiting {
		select {
		case event := <-sender.ch:
			if event.agentID != orch.ID {
				continue
			}
			if event.typ == "done" {
				waiting = false
				continue
			}
			if event.typ == "recovery" {
				status := event.data.(recoverytypes.Status)
				if status.State == recoverytypes.NeedsAttention {
					t.Fatalf("explicit continuation parked: %+v", status)
				}
			}
		case <-deadline:
			t.Fatal("turn did not complete")
		}
	}
	views = resumed.MessagesWithMeta()
	humans, partials := 0, 0
	for _, view := range views {
		if view.Msg.ContentString() == "exact task" {
			humans++
		}
		if view.Msg.ContentString() == accepted.ContentString() {
			partials++
		}
	}
	if humans != 1 || partials != 1 || requests.Load() != 1 {
		t.Fatalf("continuation duplicated or lost accepted work: humans=%d partials=%d requests=%d", humans, partials, requests.Load())
	}
}

func TestCloseRetainsUnadmittedPendingRecovery(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	s, err := NewWithSessionDir("unadmitted-recovery", testConfig(), home, directory)
	if err != nil {
		t.Fatal(err)
	}
	path, id := s.dbPath, s.sessionID
	checkpoint := sessiondb.RecoveryCheckpoint{
		Status:         recoverytypes.Status{ID: "pending-preflight", AgentID: "root", Generation: 1},
		PendingInput:   provider.PlainInput("work that has not reached the provider"),
		ContextOwnerID: id, ContextGeneration: 1, ContextReason: "pending_preflight",
	}
	if err := sessiondb.SaveRecoveryCheckpoint(s.db, id, checkpoint); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	resumed, err := ResumeSessionWithDir(path, testConfig(), home, directory)
	if err != nil {
		t.Fatalf("closing removed owned work before input admission: %v", err)
	}
	defer resumed.Close()
	if len(resumed.pendingRecoveries) != 1 {
		t.Fatal("unadmitted original input was not restored")
	}
}

func TestNaturalCompletionWaitsForDurableTurnLedger(t *testing.T) {
	checkNaturalCompletionPersistenceRetry(t, false)
}

func TestAcceptedFinalBoundaryRetriesPersistenceWithoutAnotherProviderCall(t *testing.T) {
	checkNaturalCompletionPersistenceRetry(t, true)
}

func checkNaturalCompletionPersistenceRetry(t *testing.T, boundary bool) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"accepted final answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	home := t.TempDir()
	s, err := NewWithSessionDir("finish-ledger", modelAttributionConfig(server.URL, "finish-ledger-model"), home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	trigger := `CREATE TRIGGER reject_turn_finish BEFORE UPDATE ON agent_turns
		WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT, 'ledger unavailable'); END`
	if boundary {
		trigger = `CREATE TRIGGER reject_turn_finish BEFORE INSERT ON agent_recovery_tail
			WHEN NEW.field='archive' AND instr(CAST(NEW.message_json AS TEXT), 'accepted final answer')>0
			BEGIN SELECT RAISE(ABORT, 'checkpoint unavailable'); END`
	}
	if _, err := s.db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	sender := newMockEventSender()
	orch, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !orch.SubmitInput(provider.PlainInput("finish durably")) {
		t.Fatal("input rejected")
	}
	var status recoverytypes.Status
	timeout := time.After(3 * time.Second)
wait:
	for {
		select {
		case event := <-sender.ch:
			if event.typ == "done" {
				t.Fatal("natural completion was acknowledged despite a failed turn ledger")
			}
			if event.typ == "recovery" {
				status = event.data.(recoverytypes.Status)
				if status.State == recoverytypes.NeedsAttention {
					break wait
				}
			}
		case <-timeout:
			t.Fatal("failed turn ledger was not surfaced")
		}
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_turn_finish`); err != nil {
		t.Fatal(err)
	}
	if s.RetryRecoveryFor(orch.ID, status.ID, status.Generation) != nil {
		t.Fatal("ledger retry rejected")
	}
	waitTurnDone(t, sender, orch.ID)
	if requests.Load() != 1 {
		t.Fatal("persistence retry replayed the accepted stochastic answer")
	}
	latest, _, err := sessiondb.LatestTurnStates(t.Context(), s.db, s.sessionID)
	if err != nil || latest == nil || latest.State != sessiondb.TurnCompleted {
		t.Fatalf("completion ledger not committed: %+v err=%v", latest, err)
	}
}
