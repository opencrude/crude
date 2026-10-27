package session

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	"crude/tools"
	recoverytypes "crude/types/recovery"
)

// The wrappers retain the real store, journal and notifications while putting
// publications at exact boundaries. No sleeps or test-only production hooks.
type collaborationWaitBoundaryStore struct {
	*collaboration.Store
	beforeSubscribe func()
	afterSubscribe  func()
	afterPage       func(collaboration.PageQuery, collaboration.Page)
}

func (s *collaborationWaitBoundaryStore) Subscribe() (<-chan struct{}, func()) {
	if s.beforeSubscribe != nil {
		s.beforeSubscribe()
	}
	notices, cancel := s.Store.Subscribe()
	if s.afterSubscribe != nil {
		s.afterSubscribe()
	}
	return notices, cancel
}

func (s *collaborationWaitBoundaryStore) Page(ctx context.Context, query collaboration.PageQuery) (collaboration.Page, error) {
	page, err := s.Store.Page(ctx, query)
	if err == nil && s.afterPage != nil {
		s.afterPage(query, page)
	}
	return page, err
}

func TestCollaborationWaitEveryAddressedCommunication(t *testing.T) {
	for _, kind := range []string{
		"note", "reply", "request", "revision", "accepted", "deferred", "declined",
		"interim", "final", "outcome", "human", "lifecycle", "command", "permission", "question",
	} {
		for _, boundary := range []string{"pending", "before_subscribe", "during_subscribe", "after_empty_read"} {
			t.Run(kind+"/"+boundary, func(t *testing.T) {
				s, store, recipient, sender := collaborationContractFixture(t)
				// The sender is a child, but is unrelated to the requested fact.
				publish := collaborationWaitCommunication(t, store, recipient, sender, kind)
				wrapped := &collaborationWaitBoundaryStore{Store: store}
				var event collaboration.Event
				send := func() { event = publish() }
				switch boundary {
				case "pending":
					send()
				case "before_subscribe":
					wrapped.beforeSubscribe = send
				case "during_subscribe":
					wrapped.afterSubscribe = send
				case "after_empty_read":
					wrapped.afterPage = func(query collaboration.PageQuery, page collaboration.Page) {
						if !query.Unadmitted && len(page.Events) == 0 && event.ID == "" {
							send()
						}
					}
				}
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				ctx = context.WithValue(ctx, tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: recipient.ID})
				value, err := s.waitForCollaboration(ctx, wrapped, map[string]any{
					"artifact_id": "unrelated-unpublished-artifact", "timeout_seconds": 5,
				})
				if err != nil {
					t.Fatal(err)
				}
				got := value.(collaborationWaitObservation)
				if event.ID == "" || got.Status != "directed_event" || got.Event == nil || got.Event.ID != event.ID {
					t.Fatalf("addressed %s at %s did not wake wait: %+v, published=%+v", kind, boundary, got, event)
				}
				if len(got.Unmet) != 1 || got.Unmet[0] != "artifact_revision" || got.Artifact != nil {
					t.Fatalf("communication falsely satisfied the requested fact: %+v", got)
				}
				page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: recipient.ID, Unadmitted: true})
				if err != nil || len(page.Events) != 1 || page.Events[0].ID != event.ID || page.HumanReadThrough != 0 {
					t.Fatalf("wait consumed delivery or marked human read: %+v err=%v", page, err)
				}
				pending, err := store.ReservePending(t.Context(), collaboration.ReserveRequest{
					RecipientID: recipient.ID, RuntimeEpoch: recipient.RuntimeEpoch,
				})
				if err != nil || len(pending) != 1 || pending[0].Event.ID != event.ID || pending[0].Delivery.Version != 2 {
					t.Fatalf("wait changed delivery reservation: %+v err=%v", pending, err)
				}
				state, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
				if err != nil || found || len(state.Messages) != 0 {
					t.Fatalf("wait wrote context instead of allowing normal admission: %+v found=%t err=%v", state, found, err)
				}
			})
		}
	}
}

// Prepare thread/request provenance before entering wait; only the returned
// closure publishes the communication under test.
func collaborationWaitCommunication(t *testing.T, store *collaboration.Store, recipient, sender collaboration.Agent, kind string) func() collaboration.Event {
	t.Helper()
	request := collaboration.PublishRequest{
		SenderID: sender.ID, RecipientIDs: []string{recipient.ID}, Origin: collaboration.OriginPeer,
		Kind: collaboration.EventPeerNote, IdempotencyKey: "communication", Body: "Addressed communication.",
	}
	switch kind {
	case "reply", "accepted", "deferred", "declined":
		original, err := store.Publish(t.Context(), collaboration.PublishRequest{
			SenderID: recipient.ID, RecipientIDs: []string{sender.ID}, Origin: collaboration.OriginPeer,
			Kind: collaboration.EventWorkRequest, IdempotencyKey: "original-request", Body: "Investigate.",
		})
		if err != nil {
			t.Fatal(err)
		}
		request.ReplyTo = original.Event.ID
		request.Kind = collaboration.EventPeerReply
		if kind != "reply" {
			request.Kind, request.RequestID, request.RequestRevision = collaboration.EventRequestDisposition, original.Event.RequestID, 1
			request.References = []collaboration.Reference{{Kind: "disposition", ID: kind}}
		}
	case "request":
		request.Kind = collaboration.EventWorkRequest
	case "revision":
		original, err := store.Publish(t.Context(), collaboration.PublishRequest{
			SenderID: sender.ID, RecipientIDs: []string{recipient.ID}, Origin: collaboration.OriginPeer,
			Kind: collaboration.EventWorkRequest, IdempotencyKey: "original-request", Body: "Investigate.",
		})
		if err != nil {
			t.Fatal(err)
		}
		// Retain request provenance without leaving its earlier notification
		// outstanding when the test enters wait.
		if _, err := store.Withdraw(t.Context(), collaboration.WithdrawRequest{
			EventID: original.Event.ID, SenderID: sender.ID, IdempotencyKey: "original-request", Reason: "Revision follows.",
		}); err != nil {
			t.Fatal(err)
		}
		request.Kind, request.RequestID, request.RequestRevision = collaboration.EventWorkRevision, original.Event.RequestID, 2
	case "interim", "final":
		run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
			AgentID: sender.ID, RuntimeEpoch: sender.RuntimeEpoch, Origin: collaboration.OriginPeer,
		})
		if err != nil {
			t.Fatal(err)
		}
		return func() collaboration.Event {
			report, err := store.PublishReport(t.Context(), collaboration.PublishReportRequest{
				AuthorID: sender.ID, RunID: run.ID, RecipientIDs: []string{recipient.ID},
				IdempotencyKey: "communication-report", Kind: collaboration.ReportKind(kind), Summary: "Evidence", Body: "Exact evidence.",
			})
			if err != nil {
				t.Fatal(err)
			}
			return report.Publish.Event
		}
	case "outcome":
		request.Kind, request.Origin = collaboration.EventRunOutcome, collaboration.OriginRuntime
		run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
			AgentID: sender.ID, RuntimeEpoch: sender.RuntimeEpoch, Origin: collaboration.OriginPeer,
		})
		if err != nil {
			t.Fatal(err)
		}
		request.RunID = run.ID
		return func() collaboration.Event {
			terminal, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{
				RunID: run.ID, AgentID: sender.ID, RuntimeEpoch: sender.RuntimeEpoch,
				ExpectedVersion: run.Version, State: collaboration.RunFailed, Outcome: "Exact terminal outcome.",
			})
			if err != nil {
				t.Fatal(err)
			}
			request.References = []collaboration.Reference{{Kind: "run", ID: terminal.ID, Revision: int(terminal.Version)}}
			published, err := store.Publish(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			return published.Event
		}
	case "human":
		request.Kind, request.Origin = collaboration.EventHumanMessage, collaboration.OriginHuman
	case "lifecycle":
		request.Kind, request.Origin = collaboration.EventLifecycleControl, collaboration.OriginRuntime
	case "command":
		request.Kind, request.Origin = collaboration.EventCommandOutcome, collaboration.OriginRuntime
	case "permission":
		request.Kind, request.Origin = collaboration.EventPermissionRequest, collaboration.OriginRuntime
	case "question":
		request.Kind, request.Origin = collaboration.EventQuestionRequest, collaboration.OriginRuntime
	}
	return func() collaboration.Event {
		published, err := store.Publish(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		return published.Event
	}
}

func TestCollaborationWaitConcurrentAdmissionStillWakesOnce(t *testing.T) {
	for _, boundary := range []string{"initial_pending", "before_subscribe", "during_subscribe", "after_empty_read"} {
		t.Run(boundary, func(t *testing.T) {
			s, store, recipient, sender := collaborationContractFixture(t)
			publish := collaborationWaitCommunication(t, store, recipient, sender, "note")
			wrapped := &collaborationWaitBoundaryStore{Store: store}
			var event collaboration.Event
			admit := func() {
				if event.ID == "" {
					event = publish()
				}
				deliveries, err := store.ReservePending(t.Context(), collaboration.ReserveRequest{
					RecipientID: recipient.ID, RuntimeEpoch: recipient.RuntimeEpoch,
				})
				if err != nil || len(deliveries) != 1 {
					t.Fatalf("normal reservation: %+v err=%v", deliveries, err)
				}
				message := collaborationMessage(deliveries[0])
				if _, err := s.commitCollaborationAdmissions(s.db, s.sessionID, s.sessionID, recipient.ID, "", recipient.RuntimeEpoch, 0,
					collaborationDeltas(deliveries, []provider.ChatMessage{message}, recipient.ID), deliveries); err != nil {
					t.Fatal(err)
				}
			}
			switch boundary {
			case "initial_pending":
				event = publish()
				wrapped.beforeSubscribe = admit
			case "before_subscribe":
				wrapped.beforeSubscribe = admit
			case "during_subscribe":
				wrapped.afterSubscribe = admit
			case "after_empty_read":
				wrapped.afterPage = func(query collaboration.PageQuery, page collaboration.Page) {
					if !query.Unadmitted && len(page.Events) == 0 && event.ID == "" {
						admit()
					}
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			ctx = context.WithValue(ctx, tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: recipient.ID})
			value, err := s.waitForCollaboration(ctx, wrapped, map[string]any{"artifact_id": "unrelated-artifact"})
			if err != nil {
				t.Fatal(err)
			}
			got := value.(collaborationWaitObservation)
			if got.Status != "directed_event" || got.Event == nil || got.Event.ID != event.ID {
				t.Fatalf("concurrent admission erased wake: %+v event=%s", got, event.ID)
			}
			before, _, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
			if err != nil || len(before.Messages) != 1 || before.Messages[0].InternalID != event.ID {
				t.Fatalf("normal admission did not retain exactly one message: %+v err=%v", before, err)
			}
			// A fresh wait must exclude the admitted event, not replay its wake.
			cancelCtx, stop := context.WithCancel(t.Context())
			next := &collaborationWaitBoundaryStore{Store: store}
			next.afterPage = func(query collaboration.PageQuery, page collaboration.Page) {
				if !query.Unadmitted {
					if len(page.Events) != 0 {
						t.Errorf("new wait replayed admitted history: %+v", page)
					}
					stop()
				}
			}
			cancelCtx = context.WithValue(cancelCtx, tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: recipient.ID})
			value, err = s.waitForCollaboration(cancelCtx, next, map[string]any{"artifact_id": "unrelated-artifact"})
			stop()
			if err != nil || value.(collaborationWaitObservation).Status != "caller_interrupted" || value.(collaborationWaitObservation).Event != nil {
				t.Fatalf("admitted history re-woke next wait: %+v err=%v", value, err)
			}
			after, _, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("wait changed normally admitted context: before=%+v after=%+v err=%v", before, after, err)
			}
		})
	}
}

func TestCollaborationWaitIgnoresNonaddressedAndSettledEvents(t *testing.T) {
	for _, settled := range []string{"nonaddressed", "withdrawn", "rejected", "admitting"} {
		t.Run(settled, func(t *testing.T) {
			s, store, recipient, sender := collaborationContractFixture(t)
			to := recipient.ID
			if settled == "nonaddressed" {
				to = sender.ID
			}
			published, err := store.Publish(t.Context(), collaboration.PublishRequest{
				SenderID: sender.ID, RecipientIDs: []string{to}, Origin: collaboration.OriginPeer,
				Kind: collaboration.EventPeerNote, IdempotencyKey: "settled", Body: "A note.",
			})
			if err != nil {
				t.Fatal(err)
			}
			switch settled {
			case "withdrawn":
				if _, err := store.Withdraw(t.Context(), collaboration.WithdrawRequest{
					EventID: published.Event.ID, SenderID: sender.ID, IdempotencyKey: "settled", Reason: "Not needed.",
				}); err != nil {
					t.Fatal(err)
				}
			case "rejected", "admitting":
				deliveries, err := store.ReservePending(t.Context(), collaboration.ReserveRequest{RecipientID: recipient.ID, RuntimeEpoch: recipient.RuntimeEpoch})
				if err != nil || len(deliveries) != 1 {
					t.Fatalf("reserve: %+v err=%v", deliveries, err)
				}
				if settled == "rejected" {
					if _, err := store.ReleaseReservation(t.Context(), collaboration.ReleaseRequest{
						DeliveryID: deliveries[0].Delivery.ID, RecipientID: recipient.ID, RuntimeEpoch: recipient.RuntimeEpoch,
						ExpectedVersion: deliveries[0].Delivery.Version, Reject: true, Reason: "Rejected.",
					}); err != nil {
						t.Fatal(err)
					}
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wrapped := &collaborationWaitBoundaryStore{Store: store}
			wrapped.afterPage = func(query collaboration.PageQuery, page collaboration.Page) {
				if !query.Unadmitted {
					cancel()
				}
			}
			ctx = context.WithValue(ctx, tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: recipient.ID})
			value, err := s.waitForCollaboration(ctx, wrapped, map[string]any{"artifact_id": "missing"})
			if err != nil {
				t.Fatal(err)
			}
			got := value.(collaborationWaitObservation)
			if settled == "admitting" {
				if got.Status != "directed_event" || got.Event == nil || got.Event.ID != published.Event.ID {
					t.Fatalf("reserved but uncommitted communication was skipped: %+v", got)
				}
			} else if got.Status != "caller_interrupted" || got.Event != nil {
				t.Fatalf("nonaddressed/settled event woke wait: %+v", got)
			}
		})
	}
}

func TestCollaborationWaitRecoveryPrecedesCommunicationAndTeardownDoesNotHideIt(t *testing.T) {
	for _, parking := range []bool{false, true} {
		t.Run(fmt.Sprintf("parking=%t", parking), func(t *testing.T) {
			s, store, recipient, sender := collaborationContractFixture(t)
			run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{AgentID: sender.ID, RuntimeEpoch: sender.RuntimeEpoch, Origin: collaboration.OriginPeer})
			if err != nil {
				t.Fatal(err)
			}
			event := collaborationWaitCommunication(t, store, recipient, sender, "note")()
			status := recoverytypes.Status{ID: "exact-incident", Generation: 2, State: recoverytypes.NeedsAttention, Summary: "Repair it."}
			s.recoverySlots = map[string]*recoverySlot{sender.ID: {status: status, parking: parking}}
			ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: recipient.ID})
			value, err := s.waitForCollaboration(ctx, store, map[string]any{"agent_id": sender.ID, "run_id": run.ID})
			if err != nil {
				t.Fatal(err)
			}
			got := value.(collaborationWaitObservation)
			if parking {
				if got.Status != "directed_event" || got.Event == nil || got.Event.ID != event.ID {
					t.Fatalf("teardown hid addressed communication: %+v", got)
				}
			} else if got.Status != "needs_attention" || got.Recovery == nil || got.Recovery.ID != status.ID {
				t.Fatalf("communication hid actionable recovery: %+v", got)
			}
		})
	}
}

func TestCollaborationWaitUnrelatedSenderInterruptsExactTarget(t *testing.T) {
	for _, recoveryReference := range []bool{false, true} {
		t.Run(fmt.Sprintf("recovery_reference=%t", recoveryReference), func(t *testing.T) {
			s, store, recipient, target := collaborationContractFixture(t)
			sender, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{
				ID: "unrelated-peer", Name: "Peer", SessionID: "peer-session", Category: "crafter", OwnerInstance: "peer-owner",
			})
			if err != nil {
				t.Fatal(err)
			}
			run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
				AgentID: target.ID, RuntimeEpoch: target.RuntimeEpoch, Origin: collaboration.OriginPeer,
			})
			if err != nil {
				t.Fatal(err)
			}
			wrapped := &collaborationWaitBoundaryStore{Store: store}
			var event collaboration.Event
			wrapped.afterSubscribe = func() {
				request := collaboration.PublishRequest{
					SenderID: sender.ID, RecipientIDs: []string{recipient.ID}, Origin: collaboration.OriginPeer,
					Kind: collaboration.EventPeerNote, IdempotencyKey: "unrelated-message", Body: "Something else requires handling.",
				}
				if recoveryReference {
					request.References = []collaboration.Reference{{Kind: "recovery_incident", ID: "other-incident", Revision: 1}}
				}
				published, err := store.Publish(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				event = published.Event
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			ctx = context.WithValue(ctx, tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: recipient.ID})
			value, err := s.waitForCollaboration(ctx, wrapped, map[string]any{"agent_id": target.ID, "run_id": run.ID})
			if err != nil {
				t.Fatal(err)
			}
			got := value.(collaborationWaitObservation)
			if got.Status != "directed_event" || got.Event == nil || got.Event.ID != event.ID || got.Run == nil || got.Run.State.Terminal() {
				t.Fatalf("unrelated communication was ignored or misclassified: %+v", got)
			}
		})
	}
}

func TestCollaborationWaitNonaddressedPublicationDuringWaitDoesNotInterrupt(t *testing.T) {
	s, store, recipient, other := collaborationContractFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wrapped := &collaborationWaitBoundaryStore{Store: store}
	observations := 0
	wrapped.afterPage = func(query collaboration.PageQuery, page collaboration.Page) {
		if query.Unadmitted {
			return
		}
		observations++
		if len(page.Events) != 0 {
			t.Fatalf("recipient-filtered observation leaked another recipient: %+v", page)
		}
		switch observations {
		case 1:
			if _, err := store.Publish(t.Context(), collaboration.PublishRequest{
				SenderID: recipient.ID, RecipientIDs: []string{other.ID}, Origin: collaboration.OriginPeer,
				Kind: collaboration.EventPeerNote, IdempotencyKey: "not-addressed", Body: "For another agent.",
			}); err != nil {
				t.Fatal(err)
			}
		case 2:
			cancel()
		}
	}
	ctx = context.WithValue(ctx, tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: recipient.ID})
	value, err := s.waitForCollaboration(ctx, wrapped, map[string]any{"artifact_id": "unpublished"})
	if err != nil || value.(collaborationWaitObservation).Status != "caller_interrupted" || value.(collaborationWaitObservation).Event != nil || observations != 2 {
		t.Fatalf("nonaddressed publication woke wait: %+v observations=%d err=%v", value, observations, err)
	}
}

func TestCollaborationWaitToolEntryWakesRootAndChildWithoutAdmission(t *testing.T) {
	for _, childWaiter := range []bool{false, true} {
		t.Run(fmt.Sprintf("child=%t", childWaiter), func(t *testing.T) {
			s, store, recipient, sender := collaborationContractFixture(t)
			if childWaiter {
				recipient, sender = sender, recipient
			}
			event := collaborationWaitCommunication(t, store, recipient, sender, "note")()
			ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{
				AgentID: recipient.ID, ToolCallID: "wait-addressed-communication",
			})
			result, handled, err := s.executeCollaborationTool(ctx, recipient.ID, "agent_wait", map[string]any{"artifact_id": "unrelated"})
			if err != nil || !handled {
				t.Fatalf("tool entry: handled=%t err=%v", handled, err)
			}
			var got collaborationWaitObservation
			if err := json.Unmarshal([]byte(result.Content), &got); err != nil {
				t.Fatal(err)
			}
			if got.Status != "directed_event" || got.Event == nil || got.Event.ID != event.ID {
				t.Fatalf("tool entry did not wake exact recipient: %+v", got)
			}
			pending, err := store.ReservePending(t.Context(), collaboration.ReserveRequest{RecipientID: recipient.ID, RuntimeEpoch: recipient.RuntimeEpoch})
			if err != nil || len(pending) != 1 || pending[0].Event.ID != event.ID || pending[0].Delivery.Version != 2 {
				t.Fatalf("tool entry consumed normal admission: %+v err=%v", pending, err)
			}
		})
	}
}
