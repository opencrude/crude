package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"crude/agent"
	"crude/collaboration"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/sessiondb"
	"crude/tools"
	captypes "crude/types/capability"
	"crude/types/command"
)

func (s *Session) SetCollaboration(store *collaboration.Store, instanceID string) {
	if s == nil {
		return
	}
	if store != nil && strings.TrimSpace(instanceID) == "" {
		instanceID = collaboration.NewInstanceID()
	}
	s.mu.Lock()
	if s.collaborationCancel != nil {
		s.collaborationCancel()
		s.collaborationCancel = nil
	}
	s.collaborationStore = store
	s.collaborationInstance = instanceID
	if s.collaborationAgents == nil {
		s.collaborationAgents = make(map[string]collaboration.Agent)
	}
	if store != nil {
		ctx, cancel := context.WithCancel(context.Background())
		s.collaborationCancel = cancel
		s.collaborationWg.Add(1)
		go func() {
			defer s.collaborationWg.Done()
			s.runCollaborationControls(ctx, store)
		}()
	}
	s.mu.Unlock()
}

func (s *Session) CollaborationStore() *collaboration.Store {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.collaborationStore
}

// PublishHumanSteering shares the execution owner's cancellation fence. The
// journal's run record can still be running while an explicitly stopped turn
// settles persistence; it must not accept new input on that turn's behalf.
func (s *Session) PublishHumanSteering(ctx context.Context, agentID string, request collaboration.PublishRequest) (collaboration.PublishResult, error) {
	store := s.CollaborationStore()
	if store == nil {
		return collaboration.PublishResult{}, collaboration.ErrUnavailable
	}
	s.orchMu.Lock()
	orchestrator := s.orchestrators[agentID]
	s.orchMu.Unlock()
	if orchestrator != nil {
		orchestrator.turnMu.Lock()
		defer orchestrator.turnMu.Unlock()
		if orchestrator.closing.Load() || orchestrator.turnCancel == nil || orchestrator.turnCancelling || orchestrator.turnSealed || orchestrator.RunID != request.RunID {
			return collaboration.PublishResult{}, collaboration.ErrUnavailable
		}
	} else {
		runner := s.SubAgentRunner()
		if runner == nil {
			return collaboration.PublishResult{}, collaboration.ErrUnavailable
		}
		handle, ok := runner.Manager().GetActive(agent.AgentID(agentID))
		if !ok {
			return collaboration.PublishResult{}, collaboration.ErrUnavailable
		}
		var result collaboration.PublishResult
		err := handle.WithSteeringAdmission(request.RunID, func() error {
			var err error
			result, err = store.Publish(ctx, request)
			return err
		})
		return result, err
	}
	return store.Publish(ctx, request)
}

func (s *Session) registerCollaborator(ctx context.Context, id, name, sessionID, parentID, category string, db *sql.DB) (collaboration.Agent, error) {
	if s == nil {
		return collaboration.Agent{}, fmt.Errorf("session is nil")
	}
	s.mu.Lock()
	store, instanceID := s.collaborationStore, s.collaborationInstance
	s.mu.Unlock()
	if store == nil {
		return collaboration.Agent{}, nil
	}
	if db == nil {
		return collaboration.Agent{}, errors.New("workspace agent attachment requires durable session evidence")
	}
	pending, receipts, err := collaborationAdmissionReceipts(db, sessionID, id)
	if err != nil {
		return collaboration.Agent{}, err
	}
	var pendingTerminal *collaboration.FinishRunRequest
	checkpoint, found, err := sessiondb.LoadRecoveryCheckpoint(db, sessionID, id)
	if err != nil {
		return collaboration.Agent{}, err
	}
	if found {
		intent, err := terminalIntent(checkpoint)
		if err != nil {
			return collaboration.Agent{}, err
		}
		if intent != nil && intent.Run.RunID != "" {
			pendingTerminal = &intent.Run
		}
	}
	registered, err := store.RegisterAgent(ctx, collaboration.AgentRegistration{
		ID: id, Name: name, SessionID: sessionID, ParentID: parentID,
		Category: category, State: collaboration.AgentAvailable, OwnerInstance: instanceID,
		Admissions: receipts, PendingTerminal: pendingTerminal,
	})
	if err != nil {
		return collaboration.Agent{}, err
	}
	for _, admission := range pending {
		if err := sessiondb.MarkCollaborationAdmissionPublished(db, admission.DeliveryID, admission.ContextCommitID); err != nil && s.logger != nil {
			s.logger.Warn("COLLAB", "recovered_receipt_mark_failed", "delivery_id", admission.DeliveryID, "error", err.Error())
		}
	}
	s.mu.Lock()
	s.collaborationAgents[id] = registered
	s.mu.Unlock()
	return registered, nil
}

func (s *Session) detachCollaborator(ctx context.Context, id string) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	store, instanceID := s.collaborationStore, s.collaborationInstance
	agent, ok := s.collaborationAgents[id]
	delete(s.collaborationAgents, id)
	s.mu.Unlock()
	if !ok || store == nil || agent.RuntimeEpoch == 0 {
		return
	}
	if err := store.DetachAgent(ctx, id, instanceID, agent.RuntimeEpoch); err != nil &&
		s.logger != nil {
		s.logger.Warn("COLLAB", "detach_failed", "agent_id", id, "error", err.Error())
	}
}

func (s *Session) reserveCollaboration(ctx context.Context, agentID string, runtimeEpoch uint64, limit int) ([]collaboration.PendingDelivery, error) {
	s.mu.Lock()
	store := s.collaborationStore
	s.mu.Unlock()
	if store == nil || runtimeEpoch == 0 {
		return nil, nil
	}
	return store.ReservePending(ctx, collaboration.ReserveRequest{
		RecipientID: agentID, RuntimeEpoch: runtimeEpoch, Limit: limit,
		ExcludeKinds: []collaboration.EventKind{
			collaboration.EventLifecycleControl,
			collaboration.EventHumanSteering,
		},
	})
}

func collaborationInput(delivery collaboration.PendingDelivery) provider.UserInput {
	event := delivery.Event
	references := make([]rootprompt.CollaborationReference, 0, len(event.References))
	for _, reference := range event.References {
		references = append(references, rootprompt.CollaborationReference{
			Kind: reference.Kind, ID: reference.ID, Revision: reference.Revision,
		})
	}
	text := rootprompt.CollaborationEvent(rootprompt.CollaborationEventData{
		Origin: string(event.Origin), EventID: event.ID, ThreadID: event.ThreadID, SenderID: event.SenderID,
		SenderName: event.SenderName, Kind: string(event.Kind), RequestID: event.RequestID,
		RequestRevision: event.RequestRevision, RunID: event.RunID, Body: event.Body,
		Broadcast:  event.Broadcast,
		References: references, Disposition: string(collaboration.EventDisposition(event)),
	})
	display := event.SenderName + " · " + collaborationKindLabel(event.Kind)
	if event.Broadcast {
		display = event.SenderName + " · broadcast to running agents"
	}
	if event.Body != "" {
		display += "\n" + event.Body
	}
	attribution := collaborationAttribution(event)
	return provider.UserInput{
		InternalID: event.ID, Text: text, DisplayText: display, Origin: provider.OriginPeer,
		Collaboration: attribution,
	}
}

func collaborationAttribution(event collaboration.Event) *provider.CollaborationAttribution {
	attribution := &provider.CollaborationAttribution{
		EventID: event.ID, ThreadID: event.ThreadID, SenderID: event.SenderID,
		SenderName: event.SenderName, Kind: string(event.Kind),
		RequestID: event.RequestID, RequestRevision: event.RequestRevision, RunID: event.RunID,
		Broadcast:      event.Broadcast,
		Command:        command.Clone(event.Command),
		RecipientIDs:   collaborationRecipientIDs(event.Recipients),
		RecipientNames: collaborationRecipientNames(event.Recipients),
	}
	for _, reference := range event.References {
		switch reference.Kind {
		case "history_message":
			attribution.HistoryMessageID = reference.ID
		case "report":
			attribution.ArtifactID, attribution.ArtifactRevision = reference.ID, reference.Revision
		}
	}
	return attribution
}

func collaborationRecipientNames(recipients []collaboration.Recipient) []string {
	result := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		result = append(result, recipient.Name)
	}
	return result
}

func collaborationRecipientIDs(recipients []collaboration.Recipient) []string {
	result := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		result = append(result, recipient.ID)
	}
	return result
}

func collaborationMessage(delivery collaboration.PendingDelivery) provider.ChatMessage {
	input := collaborationInput(delivery)
	message := inputMessage(provider.NewTextMessage("user", input.Text), input)
	message.InternalID = input.InternalID
	message.DisplayContent = input.DisplayText
	return message
}

func collaborationDeltas(deliveries []collaboration.PendingDelivery, messages []provider.ChatMessage, agentID string) []sessiondb.ContextMessageDelta {
	byID := make(map[string]provider.ChatMessage, len(messages))
	for _, message := range messages {
		if message.InternalID != "" {
			byID[message.InternalID] = message
		}
	}
	deltas := make([]sessiondb.ContextMessageDelta, 0, len(deliveries))
	for _, delivery := range deliveries {
		message, ok := byID[collaborationHistoryMessageID(delivery.Event)]
		if !ok {
			message = collaborationMessage(delivery)
		}
		meta := sessiondb.DisplayMeta{DisplayRole: "hidden_runtime_context", ContextOnly: true}
		if message.EffectiveVisibility() == provider.VisibilityConversation {
			// Current-context messages intentionally exclude display-only fields.
			// Re-derive the immutable user-facing label from the journal event so a
			// receipt publication is byte-identical to the earlier local admission.
			meta.DisplayRole, meta.DisplayContent = "user", collaborationInput(delivery).DisplayText
		}
		meta = canonicalizeDisplayMeta(message, meta, "", 0, delivery.Event.CreatedAt)
		deltas = append(deltas, contextDelta(message, meta, 0, delivery.Event.CreatedAt, agentID))
	}
	return deltas
}

func collaborationHistoryMessageID(event collaboration.Event) string {
	if event.Kind != collaboration.EventHumanSteering || event.Origin != collaboration.OriginHuman {
		return event.ID
	}
	for _, reference := range event.References {
		if reference.Kind == "history_message" && reference.ID != "" {
			return reference.ID
		}
	}
	return event.ID
}

func collaborationPromptReferences(event collaboration.Event) []string {
	var ids []string
	for _, reference := range event.References {
		if reference.Kind == "prompt_reference" && reference.ID != "" {
			ids = append(ids, reference.ID)
		}
	}
	return ids
}

func (s *Session) humanSteeringMessage(ctx context.Context, delivery collaboration.PendingDelivery, cfg *Config, retained ...provider.ChatMessage) (preparedInput, error) {
	event := delivery.Event
	input := provider.InputWithRefs(event.Body, collaborationPromptReferences(event))
	input.InternalID = collaborationHistoryMessageID(event)
	input.Origin = provider.OriginHumanSteering
	input.Collaboration = collaborationAttribution(event)
	prepared, prepareErr := s.prepareInput(ctx, input, cfg, nil, retained...)
	steeringText := rootprompt.HumanSteeringPrefix() + rootprompt.CollaborationEvent(rootprompt.CollaborationEventData{
		Origin: string(event.Origin), EventID: event.ID, ThreadID: event.ThreadID,
		SenderID: event.SenderID, SenderName: event.SenderName, Kind: string(event.Kind),
		RequestID: event.RequestID, RequestRevision: event.RequestRevision,
		RunID: event.RunID, Body: event.Body, Broadcast: event.Broadcast,
	})
	message := prepared.Message
	message.Content = provider.TextContent(steeringText)
	message.InternalID = input.InternalID
	message.DisplayContent = event.Body
	message.Origin = provider.OriginHumanSteering
	message.Visibility = provider.VisibilityConversation
	message.Collaboration = input.Collaboration
	prepared.Message = message
	return prepared, prepareErr
}

func (s *Session) humanSteeringMessages(
	ctx context.Context,
	db *sql.DB,
	sessionID, agentID, runID string,
	runtimeEpoch uint64,
	generation func() uint64,
	model string,
	sender EventSender,
) (<-chan agent.RuntimeMessage, <-chan struct{}, func(context.Context) error) {
	messages := make(chan agent.RuntimeMessage, 32)
	done := make(chan struct{})
	flushRequests := make(chan chan error)
	store := s.CollaborationStore()
	if store == nil || db == nil || agentID == "" || runID == "" || runtimeEpoch == 0 {
		close(messages)
		close(done)
		return messages, done, nil
	}
	notifications, unsubscribe := store.Subscribe()
	go func() {
		defer close(done)
		defer func() {
			for {
				select {
				case runtime := <-messages:
					runtime.Release("target run ended before admission")
				default:
					close(messages)
					return
				}
			}
		}()
		defer unsubscribe()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		process := func() error {
			for range cap(messages) {
				if len(messages) == cap(messages) {
					return nil
				}
				deliveries, err := store.ReservePending(ctx, collaboration.ReserveRequest{
					RecipientID: agentID, RuntimeEpoch: runtimeEpoch, Limit: 1,
					OnlyKinds: []collaboration.EventKind{collaboration.EventHumanSteering},
				})
				if errors.Is(err, collaboration.ErrUnavailable) {
					return nil
				}
				if err != nil {
					return err
				}
				if len(deliveries) == 0 {
					return nil
				}
				delivery := deliveries[0]
				runtime := s.newHumanSteeringRuntimeMessage(
					store, db, sessionID, agentID, runID, runtimeEpoch,
					generation, model, sender, delivery,
				)
				// A preserved, unfinished preparation may continue in a replacement
				// run. Its immutable original is the authority, not the old run ID.
				ownerID := agentID
				if sessionID == s.sessionID {
					ownerID = sessionID
				}
				state, _, loadErr := sessiondb.LoadContextState(db, sessionID, ownerID)
				if loadErr != nil {
					runtime.Release("cannot inspect retained steering context")
					return loadErr
				}
				retained := slices.ContainsFunc(state.Messages, func(message provider.ChatMessage) bool {
					return message.InternalID == collaborationHistoryMessageID(delivery.Event) &&
						message.EffectiveOrigin() == provider.OriginHumanSteering && message.Collaboration != nil &&
						message.Collaboration.EventID == delivery.Event.ID
				})
				if delivery.Event.Origin != collaboration.OriginHuman || (delivery.Event.RunID != runID && !retained) {
					runtime.Release("human steering target run is stale")
					continue
				}
				select {
				case messages <- runtime:
					if !retained {
						s.wakeRecovery(agentID)
					}
					continue
				case <-ctx.Done():
					runtime.Release("target run ended before admission")
					return ctx.Err()
				}
			}
			return nil
		}
		for {
			if err := process(); err != nil && ctx.Err() == nil && s.logger != nil {
				s.logger.Warn("COLLAB", "human_steering_reserve_failed", "agent_id", agentID, "error", err.Error())
			}
			select {
			case <-ctx.Done():
				return
			case <-notifications:
			case <-ticker.C:
			case reply := <-flushRequests:
				reply <- process()
			}
		}
	}()
	flush := func(flushCtx context.Context) error {
		reply := make(chan error, 1)
		select {
		case flushRequests <- reply:
		case <-flushCtx.Done():
			return flushCtx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-reply:
			return err
		case <-flushCtx.Done():
			return flushCtx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return messages, done, flush
}

func (s *Session) newHumanSteeringRuntimeMessage(
	store *collaboration.Store,
	db *sql.DB,
	sessionID, agentID, runID string,
	runtimeEpoch uint64,
	generation func() uint64,
	model string,
	sender EventSender,
	delivery collaboration.PendingDelivery,
) agent.RuntimeMessage {
	var mu sync.Mutex
	settled := false
	historyMessageID := collaborationHistoryMessageID(delivery.Event)
	contextOwnerID := agentID
	if sessionID == s.sessionID {
		contextOwnerID = sessionID
	}
	configuration := func() *Config {
		if s.runtimeProfile == nil {
			return nil
		}
		return s.runtimeProfile.GetEffectiveConfig(agentID)
	}
	state, _, loadErr := sessiondb.LoadContextState(db, sessionID, contextOwnerID)
	prepared, prepareErr := s.humanSteeringMessage(context.Background(), delivery, configuration(), state.Messages...)
	if loadErr != nil {
		prepareErr = fmt.Errorf("load human steering context for model %q: %w", model, loadErr)
	}
	message := prepared.Message
	// Display fields are projections. A journal-authored original that already
	// exists must remain byte-identical even after selection repair.
	for _, original := range state.Messages {
		if original.InternalID == historyMessageID {
			message = original
			message.DisplayContent = delivery.Event.Body
			break
		}
	}
	deltas := func() []sessiondb.ContextMessageDelta {
		messages := append([]provider.ChatMessage{message}, prepared.Enrichment...)
		result := make([]sessiondb.ContextMessageDelta, 0, len(messages))
		for _, item := range messages {
			meta := sessiondb.DisplayMeta{DisplayRole: "hidden_runtime_context", ContextOnly: true}
			if item.InternalID == historyMessageID {
				meta.DisplayRole, meta.DisplayContent, meta.ContextOnly = "user", delivery.Event.Body, false
			}
			meta = canonicalizeDisplayMeta(item, meta, "", 0, delivery.Event.CreatedAt)
			result = append(result, contextDelta(item, meta, 0, delivery.Event.CreatedAt, agentID))
		}
		return result
	}
	notify := func(typ, reason string) {
		if sender == nil {
			return
		}
		sender.SendAgentEventWithID(typ, map[string]any{
			"event_id":           delivery.Event.ID,
			"delivery_id":        delivery.Delivery.ID,
			"history_message_id": historyMessageID,
			"reason":             reason,
		}, agentID)
	}
	release := func(reason string) {
		mu.Lock()
		if settled {
			mu.Unlock()
			return
		}
		settled = true
		mu.Unlock()
		state, found, err := sessiondb.LoadContextState(db, sessionID, contextOwnerID)
		var deliveries []collaboration.PendingDelivery
		if prepareErr == nil {
			deliveries = []collaboration.PendingDelivery{delivery}
		}
		if err == nil {
			_, err = s.commitCollaborationAdmissions(db, sessionID, contextOwnerID, agentID, runID, runtimeEpoch,
				loadContextRevision(state, found), deltas(), deliveries)
		}
		if err == nil && prepareErr != nil {
			_, err = store.ReleaseReservation(context.Background(), collaboration.ReleaseRequest{
				DeliveryID: delivery.Delivery.ID, RecipientID: agentID, RuntimeEpoch: runtimeEpoch,
				ExpectedVersion: delivery.Delivery.Version, Reason: reason,
			})
		}
		if err != nil {
			mu.Lock()
			settled = false
			mu.Unlock()
			if s.logger != nil {
				s.logger.Warn("COLLAB", "human_steering_preserve_failed", "delivery_id", delivery.Delivery.ID, "error", err.Error())
			}
			return
		}
		if prepareErr != nil {
			notify("collaboration_delivery_admitted", prepareErr.Error())
		} else {
			notify("collaboration_delivery_admitted", reason)
		}
	}
	return agent.RuntimeMessage{
		Message: message,
		Prepare: func(ctx context.Context, current []provider.ChatMessage) ([]provider.ChatMessage, error) {
			mu.Lock()
			defer mu.Unlock()
			retained := append(append([]provider.ChatMessage(nil), current...), prepared.Enrichment...)
			prepared, prepareErr = s.humanSteeringMessage(context.WithoutCancel(ctx), delivery, configuration(), retained...)
			return append([]provider.ChatMessage(nil), prepared.Enrichment...), prepareErr
		},
		Admit: func(messages []provider.ChatMessage) error {
			mu.Lock()
			if settled {
				mu.Unlock()
				return collaboration.ErrStale
			}
			current, err := store.Run(context.Background(), runID)
			if err != nil || current.AgentID != agentID || current.RuntimeEpoch != runtimeEpoch || current.State.Terminal() {
				mu.Unlock()
				if err != nil {
					return err
				}
				return collaboration.ErrStale
			}
			state, found, loadErr := sessiondb.LoadContextState(db, sessionID, contextOwnerID)
			if loadErr != nil {
				mu.Unlock()
				return loadErr
			}
			var deliveries []collaboration.PendingDelivery
			if prepareErr == nil {
				deliveries = []collaboration.PendingDelivery{delivery}
			}
			_, err = s.commitCollaborationAdmissions(
				db, sessionID, contextOwnerID, agentID, runID, runtimeEpoch, loadContextRevision(state, found),
				deltas(), deliveries,
			)
			if err == nil && prepareErr == nil {
				settled = true
			}
			mu.Unlock()
			if err != nil {
				return err
			}
			notify("collaboration_delivery_admitted", "")
			notifyAttachmentIssues(sender, agentID, prepared.AttachmentIssues)
			return nil
		},
		Release: release,
	}
}

func collaborationKindLabel(kind collaboration.EventKind) string {
	switch kind {
	case collaboration.EventWorkRequest:
		return "work request"
	case collaboration.EventWorkRevision:
		return "work revision"
	case collaboration.EventRequestDisposition:
		return "request response"
	case collaboration.EventReportPublished:
		return "report"
	case collaboration.EventLifecycleControl:
		return "control"
	case collaboration.EventControlOutcome:
		return "control outcome"
	case collaboration.EventRunOutcome:
		return "run outcome"
	case collaboration.EventCommandOutcome:
		return "command outcome"
	default:
		return "peer message"
	}
}

func (s *Session) commitCollaborationAdmissions(
	db *sql.DB,
	sessionID, contextOwnerID, agentID, runID string,
	runtimeEpoch, expectedRevision uint64,
	deltas []sessiondb.ContextMessageDelta,
	deliveries []collaboration.PendingDelivery,
) (sessiondb.ContextState, error) {
	if db == nil {
		return sessiondb.ContextState{}, errors.New("collaboration admission requires a session database")
	}
	root := sessionID == s.sessionID && contextOwnerID == s.sessionID
	if len(deliveries) == 0 && len(deltas) == 0 {
		if root {
			s.msgsMu.Lock()
			defer s.msgsMu.Unlock()
		}
		state, found, err := sessiondb.LoadContextState(db, sessionID, contextOwnerID)
		if err != nil {
			return sessiondb.ContextState{}, err
		}
		if !found {
			return sessiondb.ContextState{}, errors.New("collaboration admission context is unavailable")
		}
		if state.Revision != expectedRevision {
			return sessiondb.ContextState{}, &sessiondb.ContextRevisionConflictError{
				SessionID: sessionID, AgentID: contextOwnerID, Expected: expectedRevision, Actual: state.Revision,
			}
		}
		return state, nil
	}
	s.mu.Lock()
	store := s.collaborationStore
	s.mu.Unlock()
	if store == nil {
		return sessiondb.ContextState{}, errors.New("collaboration runtime is unavailable")
	}
	contextCommitID := newMessageID()
	admissions := make([]sessiondb.CollaborationAdmission, 0, len(deliveries))
	for _, delivery := range deliveries {
		admissions = append(admissions, sessiondb.CollaborationAdmission{
			DeliveryID: delivery.Delivery.ID, EventID: delivery.Event.ID,
			SessionID: sessionID, AgentID: agentID, RuntimeEpoch: runtimeEpoch,
			DeliveryVersion: delivery.Delivery.Version, ContextCommitID: contextCommitID,
			RunID: runID,
		})
	}
	if root {
		s.msgsMu.Lock()
	}
	state, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{
		SessionID: sessionID, AgentID: contextOwnerID, RuntimeAgentID: agentID, ExpectedRevision: expectedRevision,
		Mutation: sessiondb.ContextMutation{Append: deltas}, Admissions: admissions,
	})
	if err != nil {
		if root {
			s.msgsMu.Unlock()
		}
		return sessiondb.ContextState{}, err
	}
	if root {
		s.contextRevision, s.contextGeneration, s.contextReason = state.Revision, state.Generation, state.Reason
		s.activeContext = append([]provider.ChatMessage(nil), state.Messages...)
		for _, delta := range deltas {
			present := slices.ContainsFunc(s.msgs, func(message provider.ChatMessage) bool {
				return message.InternalID == delta.Message.InternalID
			})
			if !present {
				s.msgs = append(s.msgs, delta.Message)
				s.msgTurns = append(s.msgTurns, delta.Turn)
				s.displayMetas = append(s.displayMetas, delta.Meta)
				s.createdAts = append(s.createdAts, delta.CreatedAt)
			}
		}
		s.msgsMu.Unlock()
	}
	for _, admission := range admissions {
		if _, err := store.Admit(context.Background(), collaboration.AdmitRequest{
			DeliveryID: admission.DeliveryID, RecipientID: agentID,
			RuntimeEpoch: admission.RuntimeEpoch, ExpectedVersion: admission.DeliveryVersion,
			ContextCommitID: admission.ContextCommitID,
		}); err != nil {
			if s.logger != nil {
				s.logger.Warn("COLLAB", "receipt_publish_deferred",
					"delivery_id", admission.DeliveryID, "error", err.Error())
			}
			continue
		}
		if err := sessiondb.MarkCollaborationAdmissionPublished(db, admission.DeliveryID, admission.ContextCommitID); err != nil && s.logger != nil {
			s.logger.Warn("COLLAB", "receipt_mark_failed",
				"delivery_id", admission.DeliveryID, "error", err.Error())
		}
	}
	return state, nil
}

func (s *Session) reconcileCollaborationAdmissions(db *sql.DB, agentID string) {
	if db == nil || agentID == "" {
		return
	}
	s.mu.Lock()
	store := s.collaborationStore
	s.mu.Unlock()
	if store == nil {
		return
	}
	pending, err := sessiondb.PendingCollaborationAdmissions(db, agentID)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("COLLAB", "receipt_recovery_load_failed", "agent_id", agentID, "error", err.Error())
		}
		return
	}
	for _, admission := range pending {
		if _, err := store.Admit(context.Background(), collaboration.AdmitRequest{
			DeliveryID: admission.DeliveryID, RecipientID: admission.AgentID,
			RuntimeEpoch: admission.RuntimeEpoch, ExpectedVersion: admission.DeliveryVersion,
			ContextCommitID: admission.ContextCommitID,
		}); err != nil {
			if s.logger != nil {
				s.logger.Warn("COLLAB", "receipt_recovery_failed",
					"delivery_id", admission.DeliveryID, "error", err.Error())
			}
			continue
		}
		if err := sessiondb.MarkCollaborationAdmissionPublished(db, admission.DeliveryID, admission.ContextCommitID); err != nil && s.logger != nil {
			s.logger.Warn("COLLAB", "receipt_recovery_mark_failed",
				"delivery_id", admission.DeliveryID, "error", err.Error())
		}
	}
}

func (s *Session) releaseCollaborationReservations(agentID string, runtimeEpoch uint64, deliveries []collaboration.PendingDelivery, reason string) {
	s.mu.Lock()
	store := s.collaborationStore
	s.mu.Unlock()
	if store == nil {
		return
	}
	for _, delivery := range deliveries {
		if _, err := store.ReleaseReservation(context.Background(), collaboration.ReleaseRequest{
			DeliveryID: delivery.Delivery.ID, RecipientID: agentID,
			RuntimeEpoch: runtimeEpoch, ExpectedVersion: delivery.Delivery.Version,
			Reason: reason,
		}); err != nil && s.logger != nil {
			s.logger.Warn("COLLAB", "reservation_release_failed",
				"delivery_id", delivery.Delivery.ID, "error", err.Error())
		}
	}
}

func (s *Session) runCollaborationControls(ctx context.Context, store *collaboration.Store) {
	notifications, unsubscribe := store.Subscribe()
	defer unsubscribe()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastPresenceSweep time.Time
	process := func() {
		s.mu.Lock()
		agents := make([]collaboration.Agent, 0, len(s.collaborationAgents))
		for _, registered := range s.collaborationAgents {
			agents = append(agents, registered)
		}
		s.mu.Unlock()
		if time.Since(lastPresenceSweep) >= 5*time.Second {
			_, _ = store.MarkUnresponsiveBefore(ctx, time.Now().Add(-15*time.Second))
			lastPresenceSweep = time.Now()
		}
		for _, registered := range agents {
			if registered.RuntimeEpoch == 0 || registered.Presence != collaboration.RuntimeAttached {
				continue
			}
			if registered.HeartbeatAt.IsZero() || time.Since(registered.HeartbeatAt) >= 2*time.Second {
				if err := store.Heartbeat(ctx, registered.ID, registered.OwnerInstance, registered.RuntimeEpoch); err != nil {
					if !errors.Is(err, collaboration.ErrStale) && ctx.Err() == nil && s.logger != nil {
						s.logger.Warn("COLLAB", "heartbeat_failed", "agent_id", registered.ID, "error", err.Error())
					}
					continue
				}
				s.mu.Lock()
				current := s.collaborationAgents[registered.ID]
				current.HeartbeatAt = time.Now().UTC()
				current.Presence = collaboration.RuntimeAttached
				s.collaborationAgents[registered.ID] = current
				s.mu.Unlock()
			}
			deliveries, err := store.ReservePending(ctx, collaboration.ReserveRequest{
				RecipientID: registered.ID, RuntimeEpoch: registered.RuntimeEpoch,
				Limit: 10, OnlyKinds: []collaboration.EventKind{collaboration.EventLifecycleControl},
			})
			if err != nil {
				if !errors.Is(err, collaboration.ErrUnavailable) && ctx.Err() == nil && s.logger != nil {
					s.logger.Warn("COLLAB", "control_reserve_failed",
						"agent_id", registered.ID, "error", err.Error())
				}
				continue
			}
			for _, delivery := range deliveries {
				s.applyCollaborationControl(ctx, store, registered, delivery)
			}
		}
	}
	process()
	for {
		select {
		case <-ctx.Done():
			return
		case <-notifications:
			process()
		case <-ticker.C:
			process()
		}
	}
}

type collaborationControlTarget struct {
	db           *sql.DB
	sessionID    string
	agentID      string
	runtimeEpoch uint64
	runID        string
	runVersion   uint64
	revision     uint64
	messages     []provider.ChatMessage
	root         *Orchestrator
	child        *agent.SubAgentHandle
}

func (s *Session) controlTarget(agentID string) (collaborationControlTarget, bool) {
	s.orchMu.Lock()
	root := s.orchestrators[agentID]
	s.orchMu.Unlock()
	if root != nil {
		s.msgsMu.Lock()
		messages := append([]provider.ChatMessage(nil), s.activeContext...)
		revision := s.contextRevision
		s.msgsMu.Unlock()
		runID, runVersion := root.collaborationRunIdentity()
		return collaborationControlTarget{
			db: s.db, sessionID: s.sessionID, agentID: agentID,
			runtimeEpoch: root.RuntimeEpoch, runID: runID, runVersion: runVersion,
			revision: revision, messages: messages, root: root,
		}, true
	}
	runner := s.SubAgentRunner()
	if runner == nil {
		return collaborationControlTarget{}, false
	}
	handle, ok := runner.Manager().GetActive(agent.AgentID(agentID))
	if !ok || handle.Agent == nil {
		return collaborationControlTarget{}, false
	}
	runID, runVersion := handle.RunIdentity()
	messages := append([]provider.ChatMessage(nil), handle.Agent.ActiveContext()...)
	revision := uint64(0)
	if state, found, err := sessiondb.LoadContextState(handle.DB, handle.SessionID, agentID); err == nil && found {
		messages = state.Messages
		revision = state.Revision
	}
	return collaborationControlTarget{
		db: handle.DB, sessionID: handle.SessionID, agentID: agentID,
		runtimeEpoch: handle.RuntimeEpoch, runID: runID, runVersion: runVersion,
		revision: revision,
		messages: messages,
		child:    handle,
	}, true
}

func (s *Session) applyCollaborationControl(
	ctx context.Context,
	store *collaboration.Store,
	registered collaboration.Agent,
	delivery collaboration.PendingDelivery,
) {
	target, ok := s.controlTarget(registered.ID)
	if !ok {
		s.releaseCollaborationReservations(registered.ID, registered.RuntimeEpoch,
			[]collaboration.PendingDelivery{delivery}, "runtime owner is unavailable")
		return
	}
	operation, expectedVersion, expectedEpoch, identityErr := collaboration.ControlIdentity(delivery.Event)
	if identityErr != nil {
		_, _ = store.ReleaseReservation(ctx, collaboration.ReleaseRequest{
			DeliveryID: delivery.Delivery.ID, RecipientID: registered.ID,
			RuntimeEpoch: registered.RuntimeEpoch, ExpectedVersion: delivery.Delivery.Version,
			Reason: "malformed control", Reject: true,
		})
		return
	}
	controlMessage := collaborationMessage(delivery)
	createdAt := delivery.Event.CreatedAt
	meta := canonicalizeDisplayMeta(controlMessage, sessiondb.DisplayMeta{DisplayRole: "hidden_runtime_context", ContextOnly: true}, "", len(target.messages), createdAt)
	if _, err := s.commitCollaborationAdmissions(
		target.db, target.sessionID, func() string {
			if target.sessionID == s.sessionID {
				return s.sessionID
			}
			return target.agentID
		}(), target.agentID, target.runID, target.runtimeEpoch,
		target.revision, []sessiondb.ContextMessageDelta{contextDelta(controlMessage, meta, 0, createdAt, target.agentID)}, []collaboration.PendingDelivery{delivery},
	); err != nil {
		s.releaseCollaborationReservations(registered.ID, registered.RuntimeEpoch,
			[]collaboration.PendingDelivery{delivery}, "control context admission failed")
		return
	}
	if s.sender != nil {
		s.sender.SendAgentEventWithID("collaboration_message", delivery.Event, target.agentID)
	}
	if operation == "cancel" || operation == "pause" {
		// The event's run is authoritative; a replacement with the same
		// version number must never inherit the old operation.
		if target.runID != delivery.Event.RunID {
			expectedVersion = 0
		}
		expectedEpoch = target.runtimeEpoch
	}
	outcome, applyErr := s.applyRuntimeControl(ctx, store, target, operation, expectedVersion, expectedEpoch)
	if applyErr != nil {
		outcome = "rejected: " + applyErr.Error()
	}
	if delivery.Event.SenderID == "" {
		return
	}
	recipients := []string{delivery.Event.SenderID}
	auditOnly := delivery.Event.Origin == collaboration.OriginHuman
	if auditOnly {
		recipients = nil
	}
	_, err := store.Publish(context.Background(), collaboration.PublishRequest{
		SenderID: target.agentID, SenderName: registered.Name,
		IdempotencyKey: "control-outcome:" + delivery.Event.ID,
		RecipientIDs:   recipients, AuditOnly: auditOnly,
		Origin: collaboration.OriginRuntime, Kind: collaboration.EventControlOutcome,
		Body:    strings.ToUpper(operation[:1]) + operation[1:] + " " + outcome,
		ReplyTo: delivery.Event.ID, RunID: delivery.Event.RunID,
	})
	if err != nil && s.logger != nil {
		s.logger.Warn("COLLAB", "control_outcome_publish_failed",
			"event_id", delivery.Event.ID, "error", err.Error())
	}
}

func (s *Session) applyRuntimeControl(
	ctx context.Context,
	store *collaboration.Store,
	target collaborationControlTarget,
	operation string,
	expectedVersion uint64,
	expectedEpoch uint64,
) (string, error) {
	updated, err := store.ApplyControl(ctx, collaboration.ControlRequest{
		AgentID: target.agentID, RuntimeEpoch: expectedEpoch,
		RunID: target.runID, ExpectedVersion: expectedVersion, Operation: operation,
	})
	if err != nil {
		return "", err
	}
	if updated != nil {
		if target.root != nil {
			if err := target.root.cancelCollaborationRun(updated.ID, expectedVersion, updated.Version); err != nil {
				return "", err
			}
		} else if target.child != nil {
			target.child.SetRunVersion(updated.Version)
			target.child.Cancel()
		}
	}
	return "applied", nil
}

func (s *Session) collaborationDefinitions(actorID string) []tools.ToolDef {
	s.mu.Lock()
	store := s.collaborationStore
	s.mu.Unlock()
	if store == nil {
		return nil
	}
	definitions := append([]tools.ToolDef(nil), tools.CollaborationTools...)
	participant, err := store.Agent(context.Background(), actorID)
	leaf := err == nil && participant.ParentID != ""
	if leaf || s.runtimeProfile != nil && !s.runtimeProfile.GetEffectiveConfig(actorID).IsMultiAgentEnabled() {
		filtered := definitions[:0]
		for _, definition := range definitions {
			if definition.Function.Name != "agent_start" && definition.Function.Name != "agent_configure" {
				filtered = append(filtered, definition)
			}
		}
		definitions = filtered
	}
	return definitions
}

func getBoolParam(params map[string]any, key string) bool {
	value, _ := params[key].(bool)
	return value
}

func (s *Session) executeCollaborationTool(ctx context.Context, actorID, name string, params map[string]any) (tools.ToolResult, bool, error) {
	if name == "agent_narrow_claims" || name == "agent_update_claims" {
		return s.executeClaimsTool(ctx, actorID, name, params)
	}
	s.mu.Lock()
	store := s.collaborationStore
	s.mu.Unlock()
	if store == nil {
		return tools.ToolResult{}, false, nil
	}
	switch name {
	case "agent_start":
		if participant, err := store.Agent(ctx, actorID); err != nil {
			return tools.ToolResult{}, true, err
		} else if participant.ParentID != "" {
			return tools.ToolResult{}, true, fmt.Errorf("bounded leaf agents cannot launch children; request independent work from the parent or an existing peer")
		}
		if s.runtimeProfile != nil && !s.runtimeProfile.GetEffectiveConfig(actorID).IsMultiAgentEnabled() {
			return delegationDisabledResult(), true, nil
		}
		if !s.initSubAgentRunner() {
			return delegationDisabledResult(), true, nil
		}
		result, err := s.SubAgentRunner().StartAgent(ctx, params)
		return result, true, err
	case "agent_list":
		agents, err := store.ListAgents(ctx, collaboration.AgentQuery{
			Query: getStrParam(params, "query"), SessionID: getStrParam(params, "session_id"),
			ParentID:      getStrParam(params, "parent_id"),
			State:         collaboration.AgentState(getStrParam(params, "state")),
			Presence:      collaboration.RuntimePresence(getStrParam(params, "presence")),
			IncludeClosed: getBoolParam(params, "include_closed"), Limit: getIntParam(params, "limit"),
		})
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		// A stopped agent's open incident is listed with it, so a parent never
		// mistakes a parked child for an idle or running one.
		type listedAgent struct {
			collaboration.Agent
			Recovery *agentRecoveryView `json:"recovery,omitempty"`
		}
		listed := make([]listedAgent, 0, len(agents))
		for _, participant := range agents {
			listed = append(listed, listedAgent{Agent: participant, Recovery: s.agentRecovery(participant.ID)})
		}
		return collaborationResult(listed, nil), true, nil
	case "agent_inspect":
		agentID := getStrParam(params, "agent_id")
		agentState, err := store.Agent(ctx, agentID)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		var run *collaboration.Run
		if agentState.CurrentRunID != "" {
			current, runErr := store.Run(ctx, agentState.CurrentRunID)
			if runErr != nil {
				return tools.ToolResult{}, true, runErr
			}
			run = &current
		}
		artifacts, err := store.ListArtifacts(ctx, agentID, "", "", 10)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		claims, err := store.Claims(ctx, agentID, true)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		return collaborationResult(struct {
			Agent    collaboration.Agent      `json:"agent"`
			Run      *collaboration.Run       `json:"run,omitempty"`
			Recovery *agentRecoveryView       `json:"recovery,omitempty"`
			Reports  []collaboration.Artifact `json:"reports,omitempty"`
			Claims   []collaboration.Claim    `json:"claims,omitempty"`
		}{agentState, run, s.agentRecovery(agentID), artifacts, claims}, nil), true, nil
	case "agent_send":
		identity, err := collaborationExecutionIdentity(ctx)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		kind, err := collaborationEventKind(getStrParam(params, "kind"))
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		references, err := collaborationReferences(params["references"])
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		published, err := store.Publish(ctx, collaboration.PublishRequest{
			SenderID: actorID, IdempotencyKey: identity.ToolCallID,
			RecipientIDs: toStringSlice(params["recipient_ids"]),
			Broadcast:    getBoolParam(params, "broadcast"), Origin: collaboration.OriginPeer,
			Kind: kind, Body: getStrParam(params, "body"), ReplyTo: getStrParam(params, "reply_to"),
			RequestID:       getStrParam(params, "request_id"),
			RequestRevision: uint64(max(0, getIntParam(params, "request_revision"))),
			Supersedes:      getStrParam(params, "supersedes"), References: references,
		})
		return collaborationResult(published, err), true, err
	case "agent_respond":
		identity, err := collaborationExecutionIdentity(ctx)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		disposition := getStrParam(params, "disposition")
		switch collaboration.RequestDisposition(disposition) {
		case collaboration.DispositionAccepted, collaboration.DispositionDeferred, collaboration.DispositionDeclined:
		default:
			return tools.ToolResult{}, true, fmt.Errorf("invalid request disposition %q", disposition)
		}
		body := strings.TrimSpace(getStrParam(params, "body"))
		if body == "" {
			body = strings.ToUpper(disposition[:1]) + disposition[1:] + " request " + getStrParam(params, "request_id")
		}
		published, err := store.Publish(ctx, collaboration.PublishRequest{
			SenderID: actorID, IdempotencyKey: identity.ToolCallID,
			RecipientIDs: []string{getStrParam(params, "recipient_id")},
			Origin:       collaboration.OriginPeer, Kind: collaboration.EventRequestDisposition,
			Body: body, ReplyTo: getStrParam(params, "reply_to"),
			RequestID:       getStrParam(params, "request_id"),
			RequestRevision: uint64(max(0, getIntParam(params, "request_revision"))),
			References:      []collaboration.Reference{{Kind: "disposition", ID: disposition}},
		})
		return collaborationResult(published, err), true, err
	case "agent_control":
		identity, err := collaborationExecutionIdentity(ctx)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		target := getStrParam(params, "agent_id")
		if !canControlAgent(ctx, store, actorID, target) {
			return tools.ToolResult{}, true, fmt.Errorf("agent %q cannot control unrelated agent %q", actorID, target)
		}
		participant, err := store.Agent(ctx, target)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		operation := getStrParam(params, "operation")
		// A stopped agent (waiting for attention, or parked with no live
		// execution) stays controllable: continue or cancel its exact incident.
		recoveryID := getStrParam(params, "recovery_id")
		if operation == "continue" || recoveryID != "" {
			if operation != "continue" && operation != "cancel" {
				return tools.ToolResult{}, true, fmt.Errorf("recovery_id applies only to continue or cancel")
			}
			generation := getIntParam(params, "recovery_generation")
			if recoveryID == "" || generation <= 0 {
				return tools.ToolResult{}, true, fmt.Errorf("%s of a stopped agent requires recovery_id and recovery_generation from agent_inspect", operation)
			}
			apply := s.RetryRecoveryFor
			outcome := "continued: a new attempt of the preserved task started, or the waiting run was woken; its outcome arrives through the normal run and report channels"
			if operation == "cancel" {
				apply = s.CancelRecoveryFor
				outcome = "cancelled: the task is settled and will not continue"
			}
			if err := apply(target, recoveryID, uint64(generation)); err != nil {
				return tools.ToolResult{}, true, fmt.Errorf("%s was not applied: %w", operation, err)
			}
			return collaborationResult(map[string]any{
				"agent_id": target, "operation": operation, "recovery_id": recoveryID,
				"recovery_generation": generation, "outcome": outcome,
			}, nil), true, nil
		}
		if participant.Presence != collaboration.RuntimeAttached {
			return tools.ToolResult{}, true, fmt.Errorf("%w: agent has no attached execution owner. If agent_inspect shows an open recovery incident, use operation continue or cancel with its recovery_id and recovery_generation; a finished child needs a new bounded assignment", collaboration.ErrUnavailable)
		}
		switch operation {
		case "pause", "resume", "cancel", "reopen":
		default:
			return tools.ToolResult{}, true, fmt.Errorf("invalid agent control operation %q", operation)
		}
		runID := getStrParam(params, "run_id")
		version := getIntParam(params, "expected_version")
		epoch := getIntParam(params, "runtime_epoch")
		switch operation {
		case "cancel", "pause":
			if runID == "" || version <= 0 {
				return tools.ToolResult{}, true, fmt.Errorf("pause/cancel require exact run_id and expected_version from agent_inspect")
			}
		case "resume", "reopen":
			if runID != "" || epoch <= 0 {
				return tools.ToolResult{}, true, fmt.Errorf("resume/reopen require exact runtime_epoch and no run_id; they restore availability, not execution")
			}
		}
		reason := strings.TrimSpace(getStrParam(params, "reason"))
		body := strings.ToUpper(operation[:1]) + operation[1:] + " requested"
		if reason != "" {
			body += ": " + reason
		}
		references := []collaboration.Reference{{
			Kind: "control", ID: operation, Revision: max(0, version),
		}}
		if epoch > 0 {
			references = append(references, collaboration.Reference{
				Kind: "runtime_epoch", ID: strconv.Itoa(epoch),
			})
		}
		published, err := store.Publish(ctx, collaboration.PublishRequest{
			SenderID: actorID, IdempotencyKey: identity.ToolCallID,
			RecipientIDs: []string{target}, Origin: collaboration.OriginPeer,
			Kind: collaboration.EventLifecycleControl, Body: body,
			RunID: runID, References: references,
		})
		return collaborationResult(published, err), true, err
	case "publish_report":
		identity, err := collaborationExecutionIdentity(ctx)
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		references, err := collaborationReferences(params["references"])
		if err != nil {
			return tools.ToolResult{}, true, err
		}
		metadata, _ := params["metadata"].(map[string]any)
		runID := getStrParam(params, "run_id")
		if runID == "" {
			if current, agentErr := store.Agent(ctx, actorID); agentErr == nil {
				runID = current.CurrentRunID
			}
		}
		published, err := store.PublishReport(ctx, collaboration.PublishReportRequest{
			ArtifactID: getStrParam(params, "artifact_id"), AuthorID: actorID,
			IdempotencyKey: identity.ToolCallID,
			RecipientIDs:   toStringSlice(params["recipient_ids"]),
			RunID:          runID, RequestID: getStrParam(params, "request_id"),
			Kind:    collaboration.ReportKind(getStrParam(params, "kind")),
			Summary: getStrParam(params, "summary"), Body: getStrParam(params, "body"),
			SupersedesRevision: getIntParam(params, "supersedes_revision"),
			References:         references, Metadata: metadata,
		})
		if err == nil && published.Artifact.Kind == collaboration.ReportFinal {
			if runner := s.SubAgentRunner(); runner != nil {
				if handle, ok := runner.Manager().GetActive(agent.AgentID(actorID)); ok {
					runID, _ := handle.RunIdentity()
					if published.Artifact.RunID == runID && published.Artifact.AuthorID == actorID {
						handle.SetReportArtifact(published.Artifact.ID, published.Artifact.Revision)
					}
				}
			}
		}
		return collaborationResult(published, err), true, err
	case "artifact_read":
		artifact, err := store.Artifact(ctx, getStrParam(params, "artifact_id"), getIntParam(params, "revision"))
		return collaborationResult(artifact, err), true, err
	case "agent_configure":
		value, err := s.configureChild(ctx, actorID, params)
		return collaborationResult(value, err), true, err
	case "agent_wait":
		value, err := s.waitForCollaboration(ctx, store, params)
		return collaborationResult(value, err), true, err
	case "board_read":
		page, err := store.Page(ctx, collaboration.PageQuery{
			Since: int64(getIntParam(params, "since")), Snapshot: int64(getIntParam(params, "snapshot")),
			ThreadID: getStrParam(params, "thread_id"), Search: getStrParam(params, "search"),
			SenderID: getStrParam(params, "sender_id"), RecipientID: getStrParam(params, "recipient_id"),
			Kind: collaboration.EventKind(getStrParam(params, "kind")), Limit: getIntParam(params, "limit"),
		})
		return collaborationResult(page, err), true, err
	default:
		return tools.ToolResult{}, false, nil
	}
}

func canControlAgent(ctx context.Context, store *collaboration.Store, actorID, targetID string) bool {
	if actorID == "" || targetID == "" {
		return false
	}
	if actorID == targetID {
		return true
	}
	current, err := store.Agent(ctx, targetID)
	if err != nil {
		return false
	}
	for current.ParentID != "" {
		if current.ParentID == actorID {
			return true
		}
		current, err = store.Agent(ctx, current.ParentID)
		if err != nil {
			return false
		}
	}
	return false
}

func collaborationExecutionIdentity(ctx context.Context) (tools.ExecutionIdentity, error) {
	identity, _ := ctx.Value(tools.ExecutionIdentityKey{}).(tools.ExecutionIdentity)
	if identity.ToolCallID == "" {
		return tools.ExecutionIdentity{}, fmt.Errorf("collaboration operation requires an originating tool-call identity")
	}
	return identity, nil
}

func collaborationEventKind(kind string) (collaboration.EventKind, error) {
	switch kind {
	case "note":
		return collaboration.EventPeerNote, nil
	case "reply":
		return collaboration.EventPeerReply, nil
	case "request":
		return collaboration.EventWorkRequest, nil
	case "revision":
		return collaboration.EventWorkRevision, nil
	default:
		return "", fmt.Errorf("invalid collaboration message kind %q", kind)
	}
}

func collaborationReferences(raw any) ([]collaboration.Reference, error) {
	if raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var references []collaboration.Reference
	if err := json.Unmarshal(encoded, &references); err != nil {
		return nil, err
	}
	return references, nil
}

func collaborationResult(value any, err error) tools.ToolResult {
	if err != nil {
		return tools.ToolResult{}
	}
	encoded, encodeErr := json.Marshal(value)
	if encodeErr != nil {
		return tools.ToolResult{Content: fmt.Sprintf(`{"error":%q}`, encodeErr.Error())}
	}
	return tools.ToolResult{Content: string(encoded)}
}

func collaborationEffect(name string) captypes.Effect {
	switch name {
	case "agent_list", "agent_inspect", "artifact_read", "agent_wait", "board_read":
		return captypes.EffectRead
	case "agent_start", "agent_control", "agent_update_claims", "agent_configure":
		return captypes.EffectExecute
	default:
		return captypes.EffectCommunication
	}
}
