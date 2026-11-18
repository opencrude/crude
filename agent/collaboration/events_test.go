package collaboration

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestPageUnadmittedRequiresRecipient(t *testing.T) {
	store, _ := openTestStore(t)
	if _, err := store.Page(t.Context(), PageQuery{Unadmitted: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unscoped delivery query accepted: %v", err)
	}
}

func TestPageUnadmittedObservesOnlyOutstandingRecipientObligations(t *testing.T) {
	store, _ := openTestStore(t)
	sender, recipient := registerTestAgents(t, store)
	var events []Event
	publish := func(key, to string) PublishResult {
		t.Helper()
		result, err := store.Publish(t.Context(), PublishRequest{
			SenderID: sender.ID, RecipientIDs: []string{to}, Origin: OriginPeer,
			Kind: EventPeerNote, Body: key, IdempotencyKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, result.Event)
		return result
	}
	reserve := func() PendingDelivery {
		t.Helper()
		pending, err := store.ReservePending(t.Context(), ReserveRequest{RecipientID: recipient.ID, RuntimeEpoch: recipient.RuntimeEpoch, Limit: 1})
		if err != nil || len(pending) != 1 {
			t.Fatalf("reserve: %+v err=%v", pending, err)
		}
		return pending[0]
	}
	publish("admitted", recipient.ID)
	admitted := reserve()
	if _, err := store.Admit(t.Context(), AdmitRequest{
		DeliveryID: admitted.Delivery.ID, RecipientID: recipient.ID, RuntimeEpoch: recipient.RuntimeEpoch,
		ExpectedVersion: admitted.Delivery.Version, ContextCommitID: "committed",
	}); err != nil {
		t.Fatal(err)
	}
	withdrawn := publish("withdrawn", recipient.ID)
	if _, err := store.Withdraw(t.Context(), WithdrawRequest{
		EventID: withdrawn.Event.ID, SenderID: sender.ID, IdempotencyKey: "withdrawn", Reason: "Withdraw.",
	}); err != nil {
		t.Fatal(err)
	}
	publish("rejected", recipient.ID)
	rejected := reserve()
	if _, err := store.ReleaseReservation(t.Context(), ReleaseRequest{
		DeliveryID: rejected.Delivery.ID, RecipientID: recipient.ID, RuntimeEpoch: recipient.RuntimeEpoch,
		ExpectedVersion: rejected.Delivery.Version, Reject: true, Reason: "Reject.",
	}); err != nil {
		t.Fatal(err)
	}
	old := publish("superseded", recipient.ID)
	replacement, err := store.Publish(t.Context(), PublishRequest{
		SenderID: sender.ID, RecipientIDs: []string{recipient.ID}, Origin: OriginPeer,
		Kind: EventPeerNote, Body: "Replacement.", IdempotencyKey: "replacement", Supersedes: old.Event.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, replacement.Event)
	admitting := reserve()
	pending := publish("pending", recipient.ID)
	publish("nonaddressed", sender.ID)
	snapshotDeliveries := func() []Delivery {
		t.Helper()
		var result []Delivery
		err := store.readTransaction(t.Context(), func(tx *sql.Tx) error {
			for _, event := range events {
				deliveries, err := deliveriesForEvent(t.Context(), tx, event.ID)
				if err != nil {
					return err
				}
				result = append(result, deliveries...)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshotDeliveries()
	page, err := store.Page(t.Context(), PageQuery{RecipientID: recipient.ID, Unadmitted: true, Limit: 1})
	if err != nil || len(page.Events) != 1 || page.Events[0].ID != admitting.Event.ID || !page.HasMore || page.HumanReadThrough != 0 {
		t.Fatalf("outstanding first page: %+v err=%v", page, err)
	}
	next, err := store.Page(t.Context(), PageQuery{RecipientID: recipient.ID, Unadmitted: true, Since: page.Cursor, Snapshot: page.Snapshot})
	if err != nil || len(next.Events) != 1 || next.Events[0].ID != pending.Event.ID || next.HasMore || next.Snapshot != page.Snapshot {
		t.Fatalf("outstanding stable second page: %+v err=%v", next, err)
	}
	if after := snapshotDeliveries(); !reflect.DeepEqual(before, after) {
		t.Fatalf("observation mutated delivery state:\nbefore=%+v\nafter=%+v", before, after)
	}
	history, err := store.Page(t.Context(), PageQuery{RecipientID: recipient.ID})
	if err != nil || len(history.Events) != len(events)-1 || history.HumanReadThrough != 0 {
		t.Fatalf("delivery filter altered ordinary history or human read state: %+v err=%v", history, err)
	}
}
