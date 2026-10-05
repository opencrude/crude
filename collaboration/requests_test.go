package collaboration

import (
	"errors"
	"fmt"
	"testing"
)

func TestWorkRequestsFenceAuthorRecipientRevisionAndDisposition(t *testing.T) {
	store, _ := openTestStore(t)
	root, child := registerTestAgents(t, store)
	request, err := store.Publish(t.Context(), PublishRequest{
		SenderID: root.ID, IdempotencyKey: "initial-work", RecipientIDs: []string{child.ID},
		Origin: OriginPeer, Kind: EventWorkRequest, Body: "Inspect parser ownership.",
	})
	if err != nil || request.Event.RequestID == "" || request.Event.RequestRevision != 1 {
		t.Fatalf("initial request = %+v, %v", request, err)
	}
	response := PublishRequest{
		SenderID: child.ID, IdempotencyKey: "accept-work", RecipientIDs: []string{root.ID},
		Origin: OriginPeer, Kind: EventRequestDisposition, Body: "No prose-derived status.",
		RequestID: request.Event.RequestID, RequestRevision: 1,
		References: []Reference{{Kind: "disposition", ID: "accepted"}},
	}
	for index, tc := range []struct {
		name string
		edit func(*PublishRequest)
		want error
	}{
		{"missing request", func(r *PublishRequest) { r.RequestID = "never-created" }, ErrNotFound},
		{"stale revision", func(r *PublishRequest) { r.RequestRevision = 999 }, ErrStale},
		{"wrong sender", func(r *PublishRequest) { r.SenderID = root.ID }, ErrConflict},
		{"wrong owner", func(r *PublishRequest) { r.RecipientIDs = []string{child.ID} }, ErrConflict},
		{"prose only", func(r *PublishRequest) { r.References = nil; r.Body = "Accepted" }, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempt := response
			attempt.IdempotencyKey = fmt.Sprintf("invalid-%d", index)
			tc.edit(&attempt)
			if _, err := store.Publish(t.Context(), attempt); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	accepted, err := store.Publish(t.Context(), response)
	if err != nil || accepted.Event.ThreadID != request.Event.ThreadID ||
		EventDisposition(accepted.Event) != DispositionAccepted {
		t.Fatalf("accepted response = %+v, %v", accepted, err)
	}
	if retry, err := store.Publish(t.Context(), response); err != nil || !retry.Duplicate {
		t.Fatalf("accepted retry = %+v, %v", retry, err)
	}
	revision := PublishRequest{
		SenderID: root.ID, IdempotencyKey: "revise-work", RecipientIDs: []string{child.ID},
		Origin: OriginPeer, Kind: EventWorkRevision, Body: "Inspect only the parser entry point.",
		RequestID: request.Event.RequestID, RequestRevision: 2,
	}
	for index, tc := range []struct {
		name string
		edit func(*PublishRequest)
		want error
	}{
		{"backwards revision", func(r *PublishRequest) { r.RequestRevision = 1 }, ErrStale},
		{"skipped revision", func(r *PublishRequest) { r.RequestRevision = 3 }, ErrStale},
		{"foreign author", func(r *PublishRequest) { r.SenderID = child.ID }, ErrConflict},
		{"changed recipients", func(r *PublishRequest) { r.RecipientIDs = []string{root.ID} }, ErrConflict},
		{"wrong preimage", func(r *PublishRequest) { r.Supersedes = accepted.Event.ID }, ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempt := revision
			attempt.IdempotencyKey = fmt.Sprintf("invalid-revision-%d", index)
			tc.edit(&attempt)
			if _, err := store.Publish(t.Context(), attempt); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	revised, err := store.Publish(t.Context(), revision)
	if err != nil || revised.Event.Supersedes != request.Event.ID || revised.Event.ThreadID != request.Event.ThreadID {
		t.Fatalf("revision = %+v, %v", revised, err)
	}
	state, err := store.Request(t.Context(), request.Event.RequestID)
	if err != nil || state.Revision != 2 || state.Dispositions[child.ID] != DispositionPending {
		t.Fatalf("request projection = %+v, %v", state, err)
	}
	response.IdempotencyKey = "late-accept"
	if _, err := store.Publish(t.Context(), response); !errors.Is(err, ErrStale) {
		t.Fatalf("old revision still accepted: %v", err)
	}
	response.IdempotencyKey, response.RequestRevision = "decline-revision", 2
	response.References = []Reference{{Kind: "disposition", ID: "declined"}}
	if _, err := store.Publish(t.Context(), response); err != nil {
		t.Fatal(err)
	}
	response.IdempotencyKey, response.References = "reaccept-declined", []Reference{{Kind: "disposition", ID: "accepted"}}
	if _, err := store.Publish(t.Context(), response); !errors.Is(err, ErrConflict) {
		t.Fatalf("declined request reopened without revision: %v", err)
	}
}

func TestAdmissionReceiptRetryIsExactAndDoesNotReadmit(t *testing.T) {
	store, _ := openTestStore(t)
	root, child := registerTestAgents(t, store)
	if _, err := store.Publish(t.Context(), PublishRequest{
		SenderID: root.ID, IdempotencyKey: "note", RecipientIDs: []string{child.ID},
		Origin: OriginPeer, Kind: EventPeerNote, Body: "Exact evidence.",
	}); err != nil {
		t.Fatal(err)
	}
	deliveries, err := store.ReservePending(t.Context(), ReserveRequest{RecipientID: child.ID, RuntimeEpoch: child.RuntimeEpoch})
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("reserve = %+v, %v", deliveries, err)
	}
	receipt := AdmitRequest{DeliveryID: deliveries[0].Delivery.ID, RecipientID: child.ID,
		RuntimeEpoch: child.RuntimeEpoch, ExpectedVersion: deliveries[0].Delivery.Version, ContextCommitID: "exact-commit"}
	first, err := store.Admit(t.Context(), receipt)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := store.Admit(t.Context(), receipt)
	if err != nil || retry.Version != first.Version || retry.AdmittedAt != first.AdmittedAt {
		t.Fatalf("receipt replay changed admission = %+v, %v", retry, err)
	}
	receipt.ContextCommitID = "other-commit"
	if _, err := store.Admit(t.Context(), receipt); !errors.Is(err, ErrStale) {
		t.Fatalf("changed receipt accepted: %v", err)
	}
}
