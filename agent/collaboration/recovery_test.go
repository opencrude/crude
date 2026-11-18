package collaboration

import (
	"database/sql"
	"errors"
	"testing"
)

func recoveryDelivery(t *testing.T, store *Store, id string) Delivery {
	t.Helper()
	var delivery Delivery
	if err := store.readTransaction(t.Context(), func(tx *sql.Tx) error {
		var err error
		delivery, err = loadDelivery(t.Context(), tx, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return delivery
}

func TestTakeoverReconcilesContextReceiptsAndInterruptsOnlyOldRun(t *testing.T) {
	store, db := openTestStore(t)
	parent, child := registerTestAgents(t, store)
	sibling, err := store.RegisterAgent(t.Context(), AgentRegistration{
		ID: "sibling", SessionID: "sibling-session", ParentID: parent.ID,
		OwnerInstance: "sibling-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(t.Context(), StartRunRequest{
		ID: "crashed-run", AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch,
		RequestID: "child-request", Origin: OriginPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.AcquireClaims(t.Context(), AcquireClaimsRequest{
		AgentID: child.ID, RunID: run.ID, Paths: []string{"owned/recovery.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	otherRun, err := store.StartRun(t.Context(), StartRunRequest{
		ID: "sibling-run", AgentID: sibling.ID, RuntimeEpoch: sibling.RuntimeEpoch, Origin: OriginPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	var reserved []PendingDelivery
	for _, key := range []string{"before-local-commit", "after-local-commit"} {
		if _, err := store.Publish(t.Context(), PublishRequest{
			SenderID: parent.ID, IdempotencyKey: key,
			RecipientIDs: []string{child.ID}, Origin: OriginPeer, Kind: EventPeerNote, Body: key,
		}); err != nil {
			t.Fatal(err)
		}
		batch, err := store.ReservePending(t.Context(), ReserveRequest{
			RecipientID: child.ID, RuntimeEpoch: child.RuntimeEpoch, Limit: 1,
		})
		if err != nil || len(batch) != 1 {
			t.Fatalf("reserve %q: %+v err=%v", key, batch, err)
		}
		reserved = append(reserved, batch...)
	}
	committed := reserved[1].Delivery
	receipt := AdmissionReceipt{
		DeliveryID: committed.ID, EventID: committed.EventID, RuntimeEpoch: child.RuntimeEpoch,
		DeliveryVersion: committed.Version, ContextCommitID: "local-context-commit",
	}
	// Simulate process death without DetachAgent; this releases the OS lock but
	// leaves both reservations and the run in their old durable states.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := Attach(db, testWorkspace, store.lockDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replacement.Close() })
	registration := AgentRegistration{
		ID: child.ID, Name: child.Name, SessionID: child.SessionID,
		ParentID: child.ParentID, Category: child.Category, OwnerInstance: child.OwnerInstance,
		Admissions: []AdmissionReceipt{receipt},
	}
	invalid := registration
	invalid.Admissions = []AdmissionReceipt{{DeliveryID: receipt.DeliveryID, EventID: "wrong-event",
		RuntimeEpoch: receipt.RuntimeEpoch, DeliveryVersion: receipt.DeliveryVersion,
		ContextCommitID: receipt.ContextCommitID}}
	if _, err := replacement.RegisterAgent(t.Context(), invalid); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid local receipt did not abort takeover: %v", err)
	}
	unchanged, err := replacement.Agent(t.Context(), child.ID)
	if err != nil || unchanged.RuntimeEpoch != child.RuntimeEpoch || unchanged.CurrentRunID != run.ID {
		t.Fatalf("failed recovery committed partial changes: %+v %v", unchanged, err)
	}
	recovered, err := replacement.RegisterAgent(t.Context(), registration)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.RuntimeEpoch != child.RuntimeEpoch+1 || recovered.CurrentRunID != "" || recovered.OwnerInstance != child.OwnerInstance {
		t.Fatalf("replacement identity/slot: %+v", recovered)
	}
	for index, reservation := range reserved {
		delivery := recoveryDelivery(t, replacement, reservation.Delivery.ID)
		expected := DeliveryPending
		if index == 1 {
			expected = DeliveryAdmitted
		}
		if delivery.State != expected || delivery.Version != reservation.Delivery.Version+1 {
			t.Fatalf("reservation %d = %+v; wanted %s", index, delivery, expected)
		}
		if index == 1 && delivery.ContextCommitID != receipt.ContextCommitID {
			t.Fatalf("committed context lost: %+v", delivery)
		}
	}
	if _, err := replacement.TransitionRun(t.Context(), run.ID, child.ID, child.RuntimeEpoch, run.Version, RunPaused); !errors.Is(err, ErrStale) {
		t.Fatalf("old owner transitioned after takeover: %v", err)
	}
	ended, err := replacement.Run(t.Context(), run.ID)
	if err != nil || ended.State != RunInterrupted || ended.Version != run.Version+1 || ended.EndedAt.IsZero() {
		t.Fatalf("crashed run not interrupted: %+v %v", ended, err)
	}
	active, err := replacement.Claims(t.Context(), child.ID, true)
	if err != nil || len(active) != 0 {
		t.Fatalf("old run claims still active: %+v %v", active, err)
	}
	all, err := replacement.Claims(t.Context(), child.ID, false)
	if err != nil || len(all) != 1 || all[0].ID != claims[0].ID || all[0].ReleasedAt.IsZero() {
		t.Fatalf("unrelated claims changed or old claim not released: %+v %v", all, err)
	}
	stillRunning, err := replacement.Run(t.Context(), otherRun.ID)
	if err != nil || stillRunning.State != RunRunning {
		t.Fatalf("sibling run changed: %+v %v", stillRunning, err)
	}
	var outcomeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM collaboration_events
		WHERE sender_id=? AND idempotency_key=? AND kind=?`, child.ID,
		"run-outcome:"+run.ID, EventRunOutcome).Scan(&outcomeCount); err != nil || outcomeCount != 1 {
		t.Fatalf("parent outcome = %d %v", outcomeCount, err)
	}
	if _, err := replacement.StartRun(t.Context(), StartRunRequest{
		ID: "fresh-run", AgentID: child.ID, RuntimeEpoch: recovered.RuntimeEpoch, Origin: OriginPeer,
	}); err != nil {
		t.Fatalf("new runtime unable to start: %v", err)
	}
}

func TestTakeoverRejectsReceiptForAnotherRecipientWithoutSideEffects(t *testing.T) {
	store, _ := openTestStore(t)
	parent, child := registerTestAgents(t, store)
	other, err := store.RegisterAgent(t.Context(), AgentRegistration{
		ID: "other", SessionID: "session-other", ParentID: parent.ID,
		OwnerInstance: "process-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Publish(t.Context(), PublishRequest{
		SenderID: parent.ID, IdempotencyKey: "not-child", RecipientIDs: []string{other.ID},
		Origin: OriginPeer, Kind: EventPeerNote, Body: "other-owned",
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.ReservePending(t.Context(), ReserveRequest{RecipientID: other.ID, RuntimeEpoch: other.RuntimeEpoch})
	if err != nil || len(pending) != 1 {
		t.Fatalf("other reserve: %+v %v", pending, err)
	}
	if err := store.DetachAgent(t.Context(), child.ID, child.OwnerInstance, child.RuntimeEpoch); err != nil {
		t.Fatal(err)
	}
	_, err = store.RegisterAgent(t.Context(), AgentRegistration{
		ID: child.ID, SessionID: child.SessionID, ParentID: child.ParentID,
		OwnerInstance: "replacement", Admissions: []AdmissionReceipt{{
			DeliveryID: result.Deliveries[0].ID, EventID: result.Event.ID,
			RuntimeEpoch: other.RuntimeEpoch, DeliveryVersion: pending[0].Delivery.Version,
			ContextCommitID: "other-commit",
		}},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign receipt accepted: %v", err)
	}
	agent, err := store.Agent(t.Context(), child.ID)
	if err != nil || agent.RuntimeEpoch != child.RuntimeEpoch {
		t.Fatalf("failed takeover changed epoch: %+v %v", agent, err)
	}
	delivery := recoveryDelivery(t, store, result.Deliveries[0].ID)
	if delivery.State != DeliveryAdmitting {
		t.Fatalf("foreign delivery changed: %+v", delivery)
	}
}

func TestTakeoverPreservesSuspensionAndAdmittedReceipt(t *testing.T) {
	store, db := openTestStore(t)
	parent, child := registerTestAgents(t, store)
	result, err := store.Publish(t.Context(), PublishRequest{
		SenderID: parent.ID, IdempotencyKey: "already-admitted",
		RecipientIDs: []string{child.ID}, Origin: OriginPeer, Kind: EventPeerNote, Body: "committed",
	})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := store.ReservePending(t.Context(), ReserveRequest{
		RecipientID: child.ID, RuntimeEpoch: child.RuntimeEpoch,
	})
	if err != nil || len(reserved) != 1 {
		t.Fatalf("reservation: %+v %v", reserved, err)
	}
	receipt := AdmissionReceipt{
		DeliveryID: result.Deliveries[0].ID, EventID: result.Event.ID,
		RuntimeEpoch: child.RuntimeEpoch, DeliveryVersion: reserved[0].Delivery.Version,
		ContextCommitID: "committed-before-crash",
	}
	if _, err := store.Admit(t.Context(), AdmitRequest{
		DeliveryID: receipt.DeliveryID, RecipientID: child.ID, RuntimeEpoch: receipt.RuntimeEpoch,
		ExpectedVersion: receipt.DeliveryVersion, ContextCommitID: receipt.ContextCommitID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAgentState(t.Context(), child.ID, child.RuntimeEpoch, AgentSuspended); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := Attach(db, testWorkspace, store.lockDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replacement.Close() })
	registration := AgentRegistration{
		ID: child.ID, SessionID: child.SessionID, ParentID: parent.ID,
		OwnerInstance: "same-owner", State: AgentAvailable, Admissions: []AdmissionReceipt{receipt},
	}
	recovered, err := replacement.RegisterAgent(t.Context(), registration)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != AgentSuspended || recovered.RuntimeEpoch != child.RuntimeEpoch+1 {
		t.Fatalf("registration unsuspended or reused runtime epoch: %+v", recovered)
	}
	admitted := recoveryDelivery(t, replacement, receipt.DeliveryID)
	if admitted.Version != receipt.DeliveryVersion+1 || admitted.ContextCommitID != receipt.ContextCommitID {
		t.Fatalf("admitted receipt was rewritten: %+v", admitted)
	}
	registration.Admissions = nil
	sameLive, err := replacement.RegisterAgent(t.Context(), registration)
	if err != nil || sameLive.RuntimeEpoch != recovered.RuntimeEpoch || sameLive.State != AgentSuspended {
		t.Fatalf("live re-registration changed state/epoch: %+v %v", sameLive, err)
	}
}

func TestLiveOwnerReregistrationKeepsRunningTurn(t *testing.T) {
	store, _ := openTestStore(t)
	owner, _ := registerTestAgents(t, store)
	run, err := store.StartRun(t.Context(), StartRunRequest{
		ID: "live-run", AgentID: owner.ID, RuntimeEpoch: owner.RuntimeEpoch,
		Origin: OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.RegisterAgent(t.Context(), AgentRegistration{
		ID: owner.ID, Name: owner.Name, SessionID: owner.SessionID,
		OwnerInstance: owner.OwnerInstance,
	})
	if err != nil || again.RuntimeEpoch != owner.RuntimeEpoch || again.CurrentRunID != run.ID {
		t.Fatalf("live registration displaced run: %+v %v", again, err)
	}
	stillRunning, err := store.Run(t.Context(), run.ID)
	if err != nil || stillRunning.State != RunRunning || stillRunning.Version != run.Version {
		t.Fatalf("live run was rewritten: %+v %v", stillRunning, err)
	}
}
