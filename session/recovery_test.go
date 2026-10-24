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
	if session.RetryRecoveryFor("live", live.id, live.generation) == nil {
		t.Fatal("an agent executing normally accepted a continue")
	}
	live.status = recoverytypes.Status{ID: live.id, Generation: live.generation, State: recoverytypes.NeedsAttention}
	if session.RetryRecoveryFor("live", "stale", 4) == nil {
		t.Fatal("stale incident woke the live recovery")
	}
	if session.RetryRecoveryFor("live", live.id, 3) == nil {
		t.Fatal("stale generation woke the live recovery")
	}
	if session.RetryRecoveryFor("live", live.id, live.generation) != nil {
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
		ContextReason:     "recovery_seed",
	}
	seed := checkpoint.ActiveContext[0]
	seed.InternalID = "pending-exact-task"
	checkpoint.Archive[0], checkpoint.ActiveContext[0] = seed, seed
	state, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: "session", AgentID: "session", Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "recovery_seed", Messages: checkpoint.ActiveContext}}})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.ContextRevision = state.Revision
	if err := sessiondb.SaveRecoveryCheckpoint(db, "session", checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint, _, err = sessiondb.LoadRecoveryCheckpoint(db, "session", "root")
	if err != nil {
		t.Fatal(err)
	}
	session.contextRevision = checkpoint.ContextRevision
	session.contextGeneration = checkpoint.ContextGeneration
	session.contextReason = checkpoint.ContextReason
	session.activeContext = append([]provider.ChatMessage(nil), checkpoint.ActiveContext...)
	session.pendingRecoveries["root"] = checkpoint
	if session.CancelRecoveryFor("root", checkpoint.Status.ID, 7) == nil {
		t.Fatal("stale cancellation settled the passive task")
	}
	if session.CancelRecoveryFor("root", checkpoint.Status.ID, checkpoint.Status.Generation) != nil {
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

func TestRecoveryTranscriptRetainsExactSnapshotUntilCommit(t *testing.T) {
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
	message.InternalID = "submitted-input"
	checkpoint := sessiondb.RecoveryCheckpoint{Status: recoverytypes.Status{ID: "incident", AgentID: "root", Generation: 1, UpdatedAt: time.Now()}, Archive: []provider.ChatMessage{message}, ActiveContext: []provider.ChatMessage{message}, ContextGeneration: 1}
	if _, err := db.Exec(`
		CREATE TRIGGER reject_message_flush
		BEFORE INSERT ON messages
		BEGIN SELECT RAISE(ABORT, 'blocked'); END
	`); err != nil {
		t.Fatal(err)
	}
	if err := s.restoreRecoveryTranscript(&checkpoint); err == nil {
		t.Fatal("fault-injected flush unexpectedly succeeded")
	}
	if len(s.msgs) != 0 || s.contextRevision != 0 || checkpoint.Archive[0].ContentString() != message.ContentString() {
		t.Fatalf("failed publication changed source or live state: checkpoint=%+v messages=%+v", checkpoint, s.msgs)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_message_flush`); err != nil {
		t.Fatal(err)
	}
	if err := s.restoreRecoveryTranscript(&checkpoint); err != nil {
		t.Fatal(err)
	}
	if len(s.msgs) != 1 || s.contextRevision == 0 {
		t.Fatalf("committed snapshot not published to live state: %+v", s.msgs)
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
	if s.CancelRecoveryFor("root", "old-incident", 1) == nil || replacement.Err() != nil {
		t.Fatal("old slot cancellation reached the replacement turn context")
	}
}

func TestRecoveryWakeCannotBeBankedByAnExecutingRun(t *testing.T) {
	for _, status := range []recoverytypes.Status{
		{ID: "incident", Generation: 1},
		{ID: "incident", Generation: 1, State: recoverytypes.Recovering},
		{ID: "incident", Generation: 1, State: recoverytypes.Recovered},
		{ID: "incident", Generation: 1, State: recoverytypes.Cancelled},
	} {
		for _, all := range []bool{false, true} {
			slot := &recoverySlot{id: status.ID, generation: 1, status: status, wake: make(chan struct{}, 1)}
			s := &Session{recoverySlots: map[string]*recoverySlot{"root": slot}}
			if all {
				s.wakeAllRecoveries() // A configuration publication is not Retry later.
			} else if s.wakeRecovery("root") {
				t.Fatalf("normal run accepted a future recovery wake: %+v", status)
			}
			select {
			case <-slot.wake:
				t.Fatalf("normal activity banked a wake for a later credential failure: %+v all=%v", status, all)
			default:
			}
			if s.RetryRecoveryFor("root", status.ID, 1) == nil {
				t.Fatalf("explicit Continue banked a wake while executing: %+v", status)
			}
		}
	}
	for _, status := range []recoverytypes.Status{
		{ID: "incident", State: recoverytypes.NeedsAttention},
		{ID: "incident", State: recoverytypes.Recovering, RetryAt: time.Now().Add(time.Minute)},
	} {
		slot := &recoverySlot{id: status.ID, generation: 1, status: status, wake: make(chan struct{}, 1)}
		s := &Session{recoverySlots: map[string]*recoverySlot{"root": slot}}
		if !s.wakeRecovery("root") || len(slot.wake) != 1 {
			t.Fatalf("actual waiting owner did not receive its wake: %+v", status)
		}
	}
}

func TestRootStopFencesContinuedReservationBeforeRecoveryRegistration(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sessiondb.CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	input := provider.PlainInput("exact parked input")
	input.InternalID = "parked-input"
	message := inputMessage(provider.NewTextMessage("user", input.Text), input)
	message.InternalID = input.InternalID
	state, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{
		SessionID: "session", AgentID: "session",
		Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "parked", Messages: []provider.ChatMessage{message}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := sessiondb.RecoveryCheckpoint{
		Status:       recoverytypes.Status{ID: "incident", AgentID: "root", Generation: 2, State: recoverytypes.Interrupted},
		PendingInput: input, InputAdmitted: true, Archive: state.Messages, ActiveContext: state.Messages,
		ContextOwnerID: "session", ContextRevision: state.Revision, ContextGeneration: state.Generation, ContextReason: state.Reason,
	}
	s := &Session{db: db, sessionID: "session", pendingRecoveries: map[string]sessiondb.RecoveryCheckpoint{"root": checkpoint},
		orchestrators: make(map[string]*Orchestrator), activeContext: state.Messages,
		contextRevision: state.Revision, contextGeneration: state.Generation, contextReason: state.Reason,
	}
	o := NewOrchestrator("root", s)
	s.orchestrators[o.ID] = o
	if s.RetryRecoveryFor(o.ID, checkpoint.Status.ID, checkpoint.Status.Generation) != nil {
		t.Fatal("exact Continue did not reserve the parked task")
	}
	request := <-o.inputCh
	defer o.endTurn(request.generation)
	if request.ctx.Err() != nil || s.recoverySlots[o.ID] != nil {
		t.Fatal("fixture skipped the admission-to-recovery-registration window")
	}
	s.lifecycleMu.Lock()
	result := make(chan bool, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		result <- s.CancelRecoveryFor(o.ID, checkpoint.Status.ID, checkpoint.Status.Generation) == nil
	}()
	<-started
	select {
	case <-result:
		s.lifecycleMu.Unlock()
		t.Fatal("root Stop bypassed the Continue lifecycle fence")
	case <-time.After(100 * time.Millisecond):
	}
	s.lifecycleMu.Unlock()
	select {
	case stopped := <-result:
		if !stopped || request.ctx.Err() == nil || !o.turnCancelling {
			t.Fatalf("Stop acknowledged without cancelling the continued reservation: stopped=%v error=%v", stopped, request.ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("exact Stop could not fence the continued reservation")
	}
	current, present, err := sessiondb.LoadContextState(db, "session", "session")
	if err != nil || !present || current.Revision != state.Revision || len(current.Messages) != 1 || current.Messages[0].InternalID != input.InternalID {
		t.Fatalf("reservation cancellation published a false parked settlement: %+v error=%v", current, err)
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
	observed := make(chan bool, 1)
	var orchestrator *Orchestrator
	checking := &sessionSupervisedJourneySender{mockEventSender: sender}
	checking.observe = func(typ string, data any, _ string) {
		status, ok := data.(recoverytypes.Status)
		if typ != "recovery" || !ok || status.State != recoverytypes.NeedsAttention || !strings.Contains(status.Detail, "injected owner panic") {
			return
		}
		session.recoveryMu.Lock()
		checkpoint, parked := session.pendingRecoveries[orchestrator.ID]
		slot := session.recoverySlots[orchestrator.ID]
		session.recoveryMu.Unlock()
		observed <- parked && slot == nil && checkpoint.Status.ID == status.ID && !orchestrator.TurnActive()
	}
	orchestrator, err = session.NewOrchestrator(checking)
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
	if !<-observed {
		t.Fatal("panic attention was published before exact passive retry ownership")
	}
	if first.State != recoverytypes.NeedsAttention || first.ID == "" {
		t.Fatalf("panic recovery = %+v", first)
	}
	if session.RetryRecoveryFor(orchestrator.ID, first.ID, first.Generation) != nil {
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
	if session.CancelRecoveryFor(orchestrator.ID, first.ID, first.Generation) != nil {
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
