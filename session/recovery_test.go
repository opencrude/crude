package session

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	recoverytypes "crude/types/recovery"
)

func TestRetryAndCancelRecoveryUseExactIncidentFence(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sessiondb.CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	sender := newMockEventSender()
	session := &Session{
		db: db, sessionID: "session", sender: sender,
		recoverySlots:     make(map[string]*recoverySlot),
		pendingRecoveries: make(map[string]sessiondb.RecoveryCheckpoint),
		orchestrators:     make(map[string]*Orchestrator),
	}
	live := &recoverySlot{id: "live-recovery", generation: 4, wake: make(chan struct{}, 1)}
	session.recoverySlots["live"] = live
	if session.RetryRecoveryFor("live", "stale", 4) {
		t.Fatal("stale incident woke the live recovery")
	}
	if session.RetryRecoveryFor("live", live.id, 3) {
		t.Fatal("stale generation woke the live recovery")
	}
	if !session.RetryRecoveryFor("live", live.id, live.generation) {
		t.Fatal("exact live recovery did not wake")
	}
	select {
	case <-live.wake:
	default:
		t.Fatal("exact wake was not delivered")
	}

	checkpoint := sessiondb.RecoveryCheckpoint{
		Status: recoverytypes.Status{
			ID: "passive-recovery", AgentID: "root", Generation: 8,
			State: recoverytypes.Interrupted, Attempt: 2,
			StartedAt: time.Now(), UpdatedAt: time.Now(),
			Summary: "Recovery interrupted by restart", Action: "Continue to resume this unfinished run",
		},
		PendingInput: provider.PlainInput("pending exact task"),
		Archive: []provider.ChatMessage{
			provider.NewTextMessage("user", "pending exact task"),
		},
		ActiveContext:     []provider.ChatMessage{provider.NewTextMessage("user", "pending exact task")},
		ContextOwnerID:    "session",
		ContextGeneration: 1,
	}
	if err := sessiondb.SaveRecoveryCheckpoint(db, "session", checkpoint); err != nil {
		t.Fatal(err)
	}
	session.pendingRecoveries["root"] = checkpoint
	if session.CancelRecoveryFor("root", checkpoint.Status.ID, 7) {
		t.Fatal("stale cancellation settled the passive task")
	}
	if !session.CancelRecoveryFor("root", checkpoint.Status.ID, checkpoint.Status.Generation) {
		t.Fatal("exact passive cancellation failed")
	}
	records, err := sessiondb.LoadRecoveryRecords(db, "session")
	if err != nil || len(records) != 1 || records[0].State != recoverytypes.Cancelled ||
		records[0].Action != "" || !records[0].RetryAt.IsZero() {
		t.Fatalf("cancelled recovery history = %+v err=%v", records, err)
	}
	active, found, err := sessiondb.LoadContextState(db, "session", "session")
	if err != nil || !found || len(active.Messages) == 0 ||
		active.Messages[len(active.Messages)-1].EffectiveOrigin() != provider.OriginTurnStopped {
		t.Fatalf("explicit cancel boundary missing: %+v found=%v err=%v", active, found, err)
	}
	recoveryEvent := <-sender.ch
	if recoveryEvent.typ != "recovery" ||
		recoveryEvent.data.(recoverytypes.Status).State != recoverytypes.Cancelled {
		t.Fatalf("typed cancel event = %#v", recoveryEvent)
	}
	doneEvent := <-sender.ch
	if doneEvent.typ != "done" || doneEvent.agentID != "root" {
		t.Fatalf("cancel event = %#v", doneEvent)
	}
}

func TestFlushWriteBufferRetainsExactSnapshotUntilCommit(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sessiondb.CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	s := &Session{db: db, sessionID: "session"}
	message := provider.NewTextMessage("user", "must survive failed persistence")
	s.appendToWriteBuf(
		[]provider.ChatMessage{message}, []int{1},
		[]sessiondb.DisplayMeta{{DisplayRole: "user", DisplayContent: message.ContentString()}},
	)
	if _, err := db.Exec(`
		CREATE TRIGGER reject_message_flush
		BEFORE INSERT ON messages
		BEGIN SELECT RAISE(ABORT, 'blocked'); END
	`); err != nil {
		t.Fatal(err)
	}
	if err := s.flushWriteBufSync(); err == nil {
		t.Fatal("fault-injected flush unexpectedly succeeded")
	}
	if len(s.writeBuf) != 1 || s.writeBuf[0].msg.ContentString() != message.ContentString() {
		t.Fatalf("failed flush dropped or changed buffer: %+v", s.writeBuf)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_message_flush`); err != nil {
		t.Fatal(err)
	}
	if err := s.flushWriteBufSync(); err != nil {
		t.Fatal(err)
	}
	if len(s.writeBuf) != 0 {
		t.Fatalf("acknowledged snapshot remained buffered: %+v", s.writeBuf)
	}
	loaded, _, err := sessiondb.LoadMessages(db)
	if err != nil || len(loaded) != 1 || loaded[0].ContentString() != message.ContentString() {
		t.Fatalf("committed messages=%+v err=%v", loaded, err)
	}
}

func TestRecoveryCancellationCannotHitAReplacementContext(t *testing.T) {
	replacement, cancel := context.WithCancel(t.Context())
	defer cancel()
	orch := NewOrchestrator("root", nil)
	orch.turnGeneration, orch.turnCancel = 2, cancel
	s := &Session{
		recoverySlots: map[string]*recoverySlot{"root": {id: "old-incident", generation: 1}},
		orchestrators: map[string]*Orchestrator{"root": orch},
	}
	if s.CancelRecoveryFor("root", "old-incident", 1) || replacement.Err() != nil {
		t.Fatal("old slot cancellation reached the replacement turn context")
	}
}

func TestNewInputCannotOverwriteParkedRecoveryOwnership(t *testing.T) {
	s := &Session{pendingRecoveries: map[string]sessiondb.RecoveryCheckpoint{
		"root": {Status: recoverytypes.Status{ID: "pending", AgentID: "root", Generation: 1}},
	}}
	orch := NewOrchestrator("root", s)
	if orch.SubmitInput(provider.PlainInput("new instruction")) || orch.TurnActive() {
		t.Fatal("fresh input silently replaced a parked task instead of preserving its draft/ownership")
	}
}

func TestRootTurnPanicParksTaskAndInputServiceAcceptsExactRetry(t *testing.T) {
	home := t.TempDir()
	session, err := NewWithSessionDir("panic-recovery", testConfig(), home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	sender := newMockEventSender()
	orchestrator, err := session.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	session.SetBeforeTurnHook(func(_ context.Context, _ string) error {
		if calls.Add(1) == 1 {
			panic("injected owner panic")
		}
		return errors.New("still unavailable")
	})
	if !orchestrator.SubmitInput(provider.PlainInput("preserve this exact input")) {
		t.Fatal("initial turn was not admitted")
	}
	first := waitRecoveryEvent(t, sender)
	if first.State != recoverytypes.NeedsAttention || first.ID == "" {
		t.Fatalf("panic recovery = %+v", first)
	}
	if !session.RetryRecoveryFor(orchestrator.ID, first.ID, first.Generation) {
		t.Fatal("exact passive retry was rejected")
	}
	second := waitRecoveryEvent(t, sender)
	if second.ID != first.ID || second.Generation != first.Generation ||
		second.State != recoverytypes.Recovering {
		t.Fatalf("replacement attempt did not preserve task identity: first=%+v second=%+v", first, second)
	}
	attention := waitRecoveryEvent(t, sender)
	if attention.ID != first.ID || attention.State != recoverytypes.NeedsAttention || calls.Load() < 2 {
		t.Fatalf("input service did not process retry: %+v calls=%d", attention, calls.Load())
	}
	checkpoint, ok, err := sessiondb.LoadRecoveryCheckpoint(session.DB(), session.SessionID(), orchestrator.ID)
	if err != nil || !ok {
		t.Fatalf("load retried checkpoint: ok=%v err=%v", ok, err)
	}
	foundContinuation := false
	for _, message := range checkpoint.ActiveContext {
		foundContinuation = foundContinuation ||
			strings.Contains(message.ContentString(), `runtime_recovery kind="interrupted"`)
	}
	if !foundContinuation {
		t.Fatalf("passive retry lacked canonical continuation: %+v", checkpoint.ActiveContext)
	}
	if !session.CancelRecoveryFor(orchestrator.ID, first.ID, first.Generation) {
		t.Fatal("exact active recovery cancellation failed")
	}
}

func waitRecoveryEvent(t *testing.T, sender *mockEventSender) recoverytypes.Status {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-sender.ch:
			if event.typ != "recovery" {
				continue
			}
			status, ok := event.data.(recoverytypes.Status)
			if !ok {
				t.Fatalf("recovery payload = %#v", event.data)
			}
			return status
		case <-timeout.C:
			t.Fatal("timed out waiting for recovery event")
		}
	}
}
