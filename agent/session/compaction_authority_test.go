package session

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/internal/logger"
	"crude/internal/providersim"
	"crude/types/conversation"
	"crude/workspace/fileio"
)

// compactableSession is an idle session with durable context and a scripted
// extraction connection.
func compactableSession(t *testing.T) (*Session, *atomic.Int32) {
	t.Helper()
	var extractions atomic.Int32
	server := providersim.New(t)
	server.Respond(func(providersim.Request) providersim.Turn {
		extractions.Add(1)
		return providersim.Text("<<<intent>>>\n- keep the request\n<<<work>>>\n- evidence retained\n<<<state>>>\n- clean\n<<<continuation>>>\n- None").WithoutUsage()
	})
	history := denseToolHistory()
	lg, err := logger.New(logger.Options{HomeDir: t.TempDir(), SessionID: "authority"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	db := sessiondb.OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := sessiondb.CreateSessionRow(db, "authority"); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.SaveContextState(db, "authority", "authority", sessiondb.ContextState{
		AgentID: "authority", Generation: 1, Reason: "turn_complete", Messages: history,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	profile.Categories[CategoryDefault] = CategoryConfig{AccountID: "account", Model: "authority-main-model"}
	profile.Categories[CategoryErrand] = CategoryConfig{AccountID: "account", Model: "authority-errand-model"}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	provider.SetModelContext("authority-main-model", 200_000)
	provider.SetModelContext("authority-errand-model", 200_000)
	account := cfg.Accounts["account"]
	connection := account.Connection
	connection.ProviderURL = server.BaseURL(providersim.Chat)
	account.Connection = connection
	cfg.Accounts["account"] = account
	return &Session{
		logger: lg, msgs: append([]provider.ChatMessage(nil), history...),
		activeContext: append([]provider.ChatMessage(nil), history...), contextGeneration: 1,
		cfg: cfg, sessionID: "authority", db: db,
	}, &extractions
}

// The reported failure: a running background command (or a child, or a
// finishing history snapshot) made the session look busy, and the core
// refused the human's /compact. None of them owns the context.
func TestHumanCompactionIsNotBlockedByWorkThatOnlyLooksBusy(t *testing.T) {
	s, extractions := compactableSession(t)
	manager := fileio.NewAsyncCommandManager()
	defer manager.CloseAndWait()
	s.asyncMgr = manager
	if _, err := manager.Start("long-build", promotionHelperCommand("sleep"), t.TempDir(), 0); err != nil {
		t.Fatal(err)
	}
	s.historyFinalizers.Add(1)
	defer s.historyFinalizers.Add(-1)
	if !s.IsTurnActive() {
		t.Fatal("fixture does not reproduce the busy-looking session")
	}
	result := s.HandleCompact()
	if result.Error != "" || result.Cancelled || extractions.Load() != 1 || len(s.activeContext) >= len(s.msgs) {
		t.Fatalf("compact result=%+v extractions=%d active=%d", result, extractions.Load(), len(s.activeContext))
	}
	if running := manager.List(); len(running) != 1 || running[0].Status != fileio.AsyncRunning {
		t.Fatalf("compaction disturbed the background command: %+v", running)
	}
}

// A finishing turn still holds the gate (its epilogue). The request is
// admitted at once, waits for the gate, and then compacts; new turns cannot
// start in between.
func TestHumanCompactionWaitsForFinishingTurnInsteadOfRefusing(t *testing.T) {
	s, extractions := compactableSession(t)
	s.turnGate.RLock()
	prepared, early := s.PrepareCompaction(s.sessionID)
	if early.Error != "" || prepared.Run == nil {
		s.turnGate.RUnlock()
		t.Fatalf("finishing turn refused /compact: %+v", early)
	}
	done := make(chan conversation.CompactResultMsg, 1)
	go func() { done <- prepared.Run() }()
	select {
	case <-done:
		s.turnGate.RUnlock()
		t.Fatal("compaction overlapped the turn that still owns the context")
	case <-time.After(100 * time.Millisecond):
	}
	if !s.compactionActive(s.sessionID) || !s.shouldQueueInput(s.sessionID) {
		s.turnGate.RUnlock()
		t.Fatal("waiting compaction did not hold admission for new input")
	}
	s.turnGate.RUnlock()
	select {
	case result := <-done:
		if result.Error != "" || result.Cancelled || extractions.Load() != 1 || len(s.activeContext) >= len(s.msgs) {
			t.Fatalf("compaction after the wait=%+v extractions=%d", result, extractions.Load())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("compaction did not start after the turn released the gate")
	}
}

func TestStopCancelsCompactionWaitingForTurnGate(t *testing.T) {
	s, extractions := compactableSession(t)
	s.turnGate.RLock()
	defer s.turnGate.RUnlock()
	prepared, early := s.PrepareCompaction(s.sessionID)
	if prepared.Run == nil {
		t.Fatalf("prepare=%+v", early)
	}
	done := make(chan bool, 1)
	go func() { done <- prepared.Run().Cancelled }()
	time.Sleep(20 * time.Millisecond)
	if !s.CancelCompaction(s.sessionID) {
		t.Fatal("waiting compaction did not accept Stop")
	}
	select {
	case cancelled := <-done:
		if !cancelled {
			t.Fatal("Stop did not report cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop could not interrupt the gate wait")
	}
	if extractions.Load() != 0 || s.compactionActive("") {
		t.Fatal("cancelled compaction ran or kept admission")
	}
	records := s.CompactionRecords(s.sessionID)
	if len(records) != 1 || records[0].State != sessiondb.CompactionCancelled {
		t.Fatalf("cancelled wait left no visible outcome: %+v", records)
	}
}

// A running turn accepts the request instead of refusing it. Repeats report
// the request already accepted, the run takes it exactly once, and a turn
// that has sealed its end sends later requests to the exclusive path.
func TestActiveTurnAcceptsHumanCompactionOnce(t *testing.T) {
	s, _ := compactableSession(t)
	o := NewOrchestrator(s.sessionID, s)
	s.orchestrators = map[string]*Orchestrator{s.sessionID: o}
	_, cancel := context.WithCancel(t.Context())
	defer cancel()
	o.turnGeneration, o.turnCancel = 7, cancel

	prepared, first := s.PrepareCompaction(s.sessionID)
	if prepared.Run != nil || first.Error != "" || first.OperationID == "" || first.Desc != compactionRequestedText {
		t.Fatalf("active turn result=%+v", first)
	}
	if s.compactionActive("") {
		t.Fatal("an accepted in-turn request must not queue steering input")
	}
	if _, again := s.PrepareCompaction(s.sessionID); again.OperationID != first.OperationID || again.Desc != compactionPendingText {
		t.Fatalf("repeat result=%+v", again)
	}
	if o.takeCompactionRequest(6) != "" {
		t.Fatal("another turn generation took the request")
	}
	if got := o.takeCompactionRequest(7); got != first.OperationID || o.takeCompactionRequest(7) != "" {
		t.Fatalf("take=%q want exactly once %q", got, first.OperationID)
	}
	if o.sealCompactionRequests(7) != "" {
		t.Fatal("consumed request was handed off again")
	}
	prepared, late := s.PrepareCompaction(s.sessionID)
	if prepared.Run == nil || late.Error != "" {
		t.Fatalf("request after the turn sealed its end=%+v", late)
	}
	s.releaseUnstartedCompaction(prepared.OperationID)
}

func compactableContext() []provider.ChatMessage {
	return []provider.ChatMessage{provider.NewTextMessage("user", "request")}
}
