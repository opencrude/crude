package collaboration

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEmptyReservationDoesNotAcquireWriterOrHideOwnerFences(t *testing.T) {
	store, db := openTestStore(t)
	root, child := registerTestAgents(t, store)
	publication, err := store.Publish(t.Context(), PublishRequest{SenderID: root.ID, IdempotencyKey: "peer-fact", RecipientIDs: []string{child.ID}, Origin: OriginPeer, Kind: EventPeerNote, Body: "Retained peer fact"})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback() }()
	if _, err = writer.Exec(`UPDATE collaboration_state SET last_sequence=last_sequence WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	// A different kind is pending. The control-only idle observation must stay
	// read-only even while a real independent writer owns the database.
	values, err := store.ReservePending(ctx, ReserveRequest{RecipientID: child.ID, RuntimeEpoch: child.RuntimeEpoch, OnlyKinds: []EventKind{EventLifecycleControl}})
	if err != nil || len(values) != 0 {
		t.Fatalf("empty filtered reservation waited on unrelated writer: values=%+v err=%v", values, err)
	}
	if _, err = store.ReservePending(ctx, ReserveRequest{RecipientID: child.ID, RuntimeEpoch: child.RuntimeEpoch + 1}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("empty read hid stale owner: %v", err)
	}
	if _, err = store.ReservePending(ctx, ReserveRequest{RecipientID: "missing", RuntimeEpoch: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty read hid missing owner: %v", err)
	}
	if err := writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	values, err = store.ReservePending(t.Context(), ReserveRequest{RecipientID: child.ID, RuntimeEpoch: child.RuntimeEpoch})
	if err != nil || len(values) != 1 || values[0].Event.ID != publication.Event.ID || values[0].Delivery.State != DeliveryAdmitting {
		t.Fatalf("real reservation lost its transactional effect: %+v %v", values, err)
	}
	values, err = store.ReservePending(t.Context(), ReserveRequest{RecipientID: child.ID, RuntimeEpoch: child.RuntimeEpoch})
	if err != nil || len(values) != 0 {
		t.Fatalf("reservation replayed admitted fact: %+v %v", values, err)
	}
}
