package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"crude/agent"
	"crude/collaboration"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
	recoverytypes "crude/types/recovery"
)

type recoverySlot struct {
	id         string
	generation uint64
	wake       chan struct{}
}

type recoveryRunState struct {
	mu                 sync.Mutex
	pending            provider.UserInput
	inputAdmitted      bool
	archive            []provider.ChatMessage
	active             []provider.ChatMessage
	contextOwnerID     string
	contextRevision    uint64
	contextGeneration  uint64
	contextReason      string
	ownerData          []byte
	terminalSettlement json.RawMessage
	sourceRevision     uint64
	publishedRevision  uint64
}

func newRecoveryRunState(input provider.UserInput, checkpoint *sessiondb.RecoveryCheckpoint) *recoveryRunState {
	state := &recoveryRunState{pending: cloneUserInput(input), contextGeneration: 1, contextReason: "recovery"}
	if checkpoint == nil {
		return state
	}
	state.pending = cloneUserInput(checkpoint.PendingInput)
	state.inputAdmitted = checkpoint.InputAdmitted
	state.archive = append([]provider.ChatMessage(nil), checkpoint.Archive...)
	state.active = append([]provider.ChatMessage(nil), checkpoint.ActiveContext...)
	state.contextOwnerID = checkpoint.ContextOwnerID
	state.contextRevision = checkpoint.ContextRevision
	state.contextGeneration = checkpoint.ContextGeneration
	state.contextReason = checkpoint.ContextReason
	state.ownerData = append([]byte(nil), checkpoint.OwnerData...)
	state.terminalSettlement = append(json.RawMessage(nil), checkpoint.TerminalSettlement...)
	state.sourceRevision = 1
	state.publishedRevision = 1
	return state
}

func (s *recoveryRunState) snapshot(archive, active []provider.ChatMessage) {
	s.mu.Lock()
	// The recovery state's existing archive is the canonical accepted source for
	// every stable message identity already observed. Provider request projection
	// may omit local display/reference/provenance fields from those prefix messages;
	// never let that lossy wire copy replace the exact transcript-compatible bytes.
	canonicalArchive := make(map[string]provider.ChatMessage, len(s.archive))
	for _, message := range s.archive {
		if message.InternalID != "" {
			canonicalArchive[message.InternalID] = message
		}
	}
	canonicalActive := make(map[string]provider.ChatMessage, len(s.active))
	for _, message := range s.active {
		if message.InternalID != "" {
			canonicalActive[message.InternalID] = message
		}
	}
	archive = retainCanonicalMessages(archive, canonicalArchive)
	active = canonicalContextMessages(retainCanonicalMessages(active, canonicalActive))
	changed := !reflect.DeepEqual(s.archive, archive) || !reflect.DeepEqual(s.active, active)
	s.archive = append([]provider.ChatMessage(nil), archive...)
	s.active = append([]provider.ChatMessage(nil), active...)
	if changed {
		s.sourceRevision++
	}
	s.mu.Unlock()
}

func retainCanonicalMessages(messages []provider.ChatMessage, canonical map[string]provider.ChatMessage) []provider.ChatMessage {
	result := append([]provider.ChatMessage(nil), messages...)
	for i, message := range result {
		if source, ok := canonical[message.InternalID]; ok {
			result[i] = source
		}
	}
	return result
}

func (s *recoveryRunState) setInputAdmitted(admitted bool) {
	s.mu.Lock()
	s.inputAdmitted = admitted
	s.mu.Unlock()
}

func (s *recoveryRunState) setPending(input provider.UserInput) {
	s.mu.Lock()
	s.pending = cloneUserInput(input)
	s.mu.Unlock()
}

func (s *recoveryRunState) setContext(owner string, revision, generation uint64, reason string) {
	s.mu.Lock()
	s.contextOwnerID = owner
	s.contextRevision = revision
	s.contextGeneration = max(uint64(1), generation)
	if reason != "" {
		s.contextReason = reason
	}
	// The caller obtained this context identity by publishing the snapshot now
	// held by this state. Mark that exact source revision acknowledged; later
	// snapshots/continuations increment sourceRevision and require replacement.
	s.publishedRevision = s.sourceRevision
	s.mu.Unlock()
}

func (s *recoveryRunState) appendContinuation(kind rootprompt.RecoveryKind) {
	message := provider.NewTextMessage("user", rootprompt.RecoveryContinuation(kind))
	message.InternalID = newMessageID()
	message.Origin = provider.OriginContinuation
	message.Visibility = provider.VisibilityContextOnly
	s.mu.Lock()
	s.archive = append(s.archive, message)
	s.active = append(s.active, message)
	s.sourceRevision++
	s.mu.Unlock()
}

func (s *recoveryRunState) contexts() (archive, active []provider.ChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]provider.ChatMessage(nil), s.archive...),
		append([]provider.ChatMessage(nil), s.active...)
}

func (s *recoveryRunState) contextState() (revision, generation uint64, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contextRevision, s.contextGeneration, s.contextReason
}

func (s *recoveryRunState) contextRevisionValue() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contextRevision
}

func (s *recoveryRunState) checkpoint(status recoverytypes.Status) sessiondb.RecoveryCheckpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sessiondb.RecoveryCheckpoint{
		Status: status, PendingInput: cloneUserInput(s.pending), InputAdmitted: s.inputAdmitted,
		Archive:        append([]provider.ChatMessage(nil), s.archive...),
		ActiveContext:  append([]provider.ChatMessage(nil), s.active...),
		ContextOwnerID: s.contextOwnerID, ContextRevision: s.contextRevision, ContextGeneration: s.contextGeneration,
		ContextReason: s.contextReason, OwnerData: append([]byte(nil), s.ownerData...),
		TerminalSettlement: append(json.RawMessage(nil), s.terminalSettlement...),
	}
}

func (s *recoveryRunState) publish(db *sql.DB, sessionID string, status recoverytypes.Status) error {
	s.mu.Lock()
	checkpoint := sessiondb.RecoveryCheckpoint{
		Status: status, PendingInput: cloneUserInput(s.pending), InputAdmitted: s.inputAdmitted,
		Archive: append([]provider.ChatMessage(nil), s.archive...), ActiveContext: append([]provider.ChatMessage(nil), s.active...),
		ContextOwnerID: s.contextOwnerID, ContextRevision: s.contextRevision, ContextGeneration: s.contextGeneration,
		ContextReason: s.contextReason, OwnerData: append([]byte(nil), s.ownerData...),
		TerminalSettlement: append(json.RawMessage(nil), s.terminalSettlement...),
	}
	owner, revision := s.contextOwnerID, s.contextRevision
	sourceRevision := s.sourceRevision
	dirty := sourceRevision != s.publishedRevision
	s.mu.Unlock()
	if owner == "" {
		return sessiondb.SaveRecoveryCheckpoint(db, sessionID, checkpoint)
	}
	publication := sessiondb.ContextPublication{SessionID: sessionID, AgentID: owner, RuntimeAgentID: status.AgentID, ExpectedRevision: revision, Recovery: &checkpoint}
	if dirty {
		// The active context snapshot is the canonical semantic source. Do not
		// manufacture display metadata or timestamps for its tail: agent checkpoint
		// publication may already have committed those messages to the immutable
		// transcript with their real provenance. A revision-fenced replacement
		// validates their stable IDs/bytes against that transcript and only advances
		// the current-context owner plus recovery checkpoint atomically.
		publication.Mutation.Replace = &sessiondb.ContextReplacement{
			Generation: max(uint64(1), checkpoint.ContextGeneration), Reason: checkpoint.ContextReason, Messages: checkpoint.ActiveContext,
		}
		if publication.Mutation.Replace.Reason == "" {
			publication.Mutation.Replace.Reason = "run_recovery"
		}
	}
	state, err := sessiondb.PublishContext(db, publication)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.contextRevision, s.contextGeneration, s.contextReason = state.Revision, state.Generation, state.Reason
	s.active = append([]provider.ChatMessage(nil), state.Messages...)
	if s.sourceRevision == sourceRevision {
		s.publishedRevision = sourceRevision
	}
	s.mu.Unlock()
	return nil
}

func cloneUserInput(input provider.UserInput) provider.UserInput {
	result := input
	result.ReferenceIDs = append([]string(nil), input.ReferenceIDs...)
	result.ContextParts = append([]string(nil), input.ContextParts...)
	result.ActivePaths = append([]string(nil), input.ActivePaths...)
	if input.Collaboration != nil {
		copy := *input.Collaboration
		copy.RecipientIDs = append([]string(nil), input.Collaboration.RecipientIDs...)
		copy.RecipientNames = append([]string(nil), input.Collaboration.RecipientNames...)
		result.Collaboration = &copy
	}
	return result
}

func recoveryArchiveMessages(messages []provider.ChatMessage) []provider.ChatMessage {
	if len(messages) > 0 && messages[0].Role == "system" {
		return append([]provider.ChatMessage(nil), messages[1:]...)
	}
	return append([]provider.ChatMessage(nil), messages...)
}

func (s *Session) newRecoverySupervisor(
	db *sql.DB,
	sessionID, agentID string,
	generation uint64,
	state *recoveryRunState,
	initial *recoverytypes.Status,
	sender EventSender,
) (*agent.Supervisor, error) {
	supervisor := agent.NewSupervisor(agentID, generation, initial)
	slot := &recoverySlot{id: supervisor.IncidentID(), generation: generation, wake: make(chan struct{}, 1)}
	supervisor.Wake = slot.wake
	supervisor.Emit = func(event agent.AgentEvent) {
		if sender != nil {
			sender.SendAgentEventWithID(event.Type, event.Data, agentID)
		}
	}
	supervisor.Persist = func(status recoverytypes.Status) error {
		state.mu.Lock()
		owner := state.contextOwnerID
		state.mu.Unlock()
		if db == s.db && owner == s.sessionID {
			return s.publishRootRecovery(state, status)
		}
		return state.publish(db, sessionID, status)
	}
	supervisor.Discard = func(id string) error {
		return sessiondb.DeleteRecoveryCheckpoint(db, id)
	}
	supervisor.LogPersist = func(err error) {
		if s != nil && s.logger != nil {
			s.logger.Warn("RECOVERY", "checkpoint_failed", "agent_id", agentID, "error", err.Error())
		}
	}
	s.recoveryMu.Lock()
	if s.recoverySlots == nil {
		s.recoverySlots = make(map[string]*recoverySlot)
	}
	if current := s.recoverySlots[agentID]; current != nil &&
		(current.id != slot.id || current.generation != generation) {
		s.recoveryMu.Unlock()
		return nil, context.Canceled
	}
	s.recoverySlots[agentID] = slot
	s.recoveryMu.Unlock()
	return supervisor, nil
}

func (s *Session) persistPendingRecovery(supervisor *agent.Supervisor) error {
	if supervisor == nil {
		return nil
	}
	return supervisor.Persist(supervisor.CheckpointStatus())
}

// Retry the same accepted boundary, not the stochastic provider request or tool
// round that produced it. This runs only on the owning off-UI execution thread.
func persistRecoveryBoundary(ctx context.Context, supervisor *agent.Supervisor) error {
	for {
		if err := supervisor.Persist(supervisor.CheckpointStatus()); err != nil {
			if !supervisor.Await(ctx, agent.AttentionRecovery(agent.RecoveryFailureStorage, err)) {
				return ctx.Err()
			}
			continue
		}
		return nil
	}
}

func (s *Session) finishRecovery(agentID, incidentID string, generation uint64) {
	if s == nil {
		return
	}
	s.recoveryMu.Lock()
	if slot := s.recoverySlots[agentID]; slot != nil &&
		slot.id == incidentID && slot.generation == generation {
		delete(s.recoverySlots, agentID)
	}
	if checkpoint, ok := s.pendingRecoveries[agentID]; ok &&
		checkpoint.Status.ID == incidentID && checkpoint.Status.Generation == generation {
		delete(s.pendingRecoveries, agentID)
	}
	s.recoveryMu.Unlock()
}

func (s *Session) parkRecovery(checkpoint sessiondb.RecoveryCheckpoint) {
	if s == nil || checkpoint.Status.AgentID == "" {
		return
	}
	s.recoveryMu.Lock()
	if s.pendingRecoveries == nil {
		s.pendingRecoveries = make(map[string]sessiondb.RecoveryCheckpoint)
	}
	delete(s.recoverySlots, checkpoint.Status.AgentID)
	s.pendingRecoveries[checkpoint.Status.AgentID] = checkpoint
	s.recoveryMu.Unlock()
	if checkpoint.Status.State == recoverytypes.NeedsAttention {
		s.publishChildNeedsAttention(checkpoint)
	}
}

func (s *Session) publishChildNeedsAttention(checkpoint sessiondb.RecoveryCheckpoint) {
	if s == nil || len(checkpoint.OwnerData) == 0 {
		return
	}
	var owner childRecoveryOwner
	if json.Unmarshal(checkpoint.OwnerData, &owner) != nil || owner.Spec.ParentID == "" {
		return
	}
	store := s.CollaborationStore()
	if store == nil {
		return
	}
	status := checkpoint.Status
	remedy := status.Action
	if remedy == "" {
		remedy = "Inspect the child incident, repair its configuration/guidance/storage cause, then explicitly retry or Stop the exact incident."
	}
	body := fmt.Sprintf("%s (%s, %s) needs attention; incident %s generation %d. %s Remedy: %s",
		owner.Spec.Name, owner.Spec.Category, owner.Spec.ID, status.ID, status.Generation, status.Detail, remedy)
	_, err := store.Publish(context.Background(), collaboration.PublishRequest{
		SenderID: string(owner.Spec.ID), SenderName: owner.Spec.Name,
		IdempotencyKey: fmt.Sprintf("needs-attention:%s:%d:%d", status.ID, status.Generation, status.UpdatedAt.UnixNano()),
		RecipientIDs:   []string{string(owner.Spec.ParentID)}, Origin: collaboration.OriginRuntime,
		Kind: collaboration.EventPeerNote, Body: body, RunID: "",
		References: []collaboration.Reference{{Kind: "recovery_incident", ID: status.ID, Revision: int(status.Generation)}},
	})
	if err != nil && s.logger != nil {
		s.logger.Warn("COLLAB", "child_attention_publish_failed", "agent_id", status.AgentID, "error", err.Error())
	}
}

func (s *Session) wakeRecovery(agentID string) bool {
	if s == nil || agentID == "" {
		return false
	}
	s.recoveryMu.Lock()
	slot := s.recoverySlots[agentID]
	s.recoveryMu.Unlock()
	if slot == nil {
		return false
	}
	select {
	case slot.wake <- struct{}{}:
	default:
	}
	return true
}

func (s *Session) wakeAllRecoveries() {
	if s == nil {
		return
	}
	s.recoveryMu.Lock()
	slots := make([]*recoverySlot, 0, len(s.recoverySlots))
	for _, slot := range s.recoverySlots {
		slots = append(slots, slot)
	}
	s.recoveryMu.Unlock()
	for _, slot := range slots {
		select {
		case slot.wake <- struct{}{}:
		default:
		}
	}
}

// RetryRecovery wakes the current exact incident for an agent.
func (s *Session) RetryRecovery(agentID string) bool {
	if s == nil || agentID == "" {
		return false
	}
	s.recoveryMu.Lock()
	if slot := s.recoverySlots[agentID]; slot != nil {
		id, generation := slot.id, slot.generation
		s.recoveryMu.Unlock()
		return s.RetryRecoveryFor(agentID, id, generation)
	}
	checkpoint, ok := s.pendingRecoveries[agentID]
	s.recoveryMu.Unlock()
	if !ok {
		return false
	}
	return s.RetryRecoveryFor(agentID, checkpoint.Status.ID, checkpoint.Status.Generation)
}

// RetryRecoveryFor fences asynchronous UI actions to one exact incident and
// generation. A stale action cannot wake replacement work.
func (s *Session) RetryRecoveryFor(agentID, recoveryID string, generation uint64) bool {
	if s == nil || agentID == "" || recoveryID == "" || generation == 0 {
		return false
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.recoveryMu.Lock()
	if slot := s.recoverySlots[agentID]; slot != nil {
		if slot.id != recoveryID || slot.generation != generation {
			s.recoveryMu.Unlock()
			return false
		}
		wake := slot.wake
		s.recoveryMu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
		return true
	}
	checkpoint, ok := s.pendingRecoveries[agentID]
	if !ok || checkpoint.Status.ID != recoveryID || checkpoint.Status.Generation != generation {
		s.recoveryMu.Unlock()
		return false
	}
	s.recoveryMu.Unlock()

	if len(checkpoint.TerminalSettlement) != 0 {
		return s.retryTerminalSettlement(checkpoint)
	}
	s.orchMu.Lock()
	orchestrator := s.orchestrators[agentID]
	s.orchMu.Unlock()
	if orchestrator != nil && checkpoint.ContextOwnerID == s.sessionID {
		return orchestrator.submitRecovery(checkpoint)
	}
	if runner := s.SubAgentRunner(); runner != nil {
		return runner.ResumeRecovery(checkpoint)
	}
	return false
}

// CancelRecoveryFor settles one exact unfinished task without starting a new
// provider/tool/child attempt. Stale UI actions cannot cancel a replacement.
func (s *Session) CancelRecoveryFor(agentID, recoveryID string, generation uint64) bool {
	if s == nil || agentID == "" || recoveryID == "" || generation == 0 {
		return false
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.recoveryMu.Lock()
	if slot := s.recoverySlots[agentID]; slot != nil {
		if slot.id != recoveryID || slot.generation != generation {
			s.recoveryMu.Unlock()
			return false
		}
		s.recoveryMu.Unlock()
		s.orchMu.Lock()
		orchestrator := s.orchestrators[agentID]
		s.orchMu.Unlock()
		if orchestrator != nil {
			cancelled, _ := orchestrator.cancelTurnForGeneration(generation)
			return cancelled
		}
		if runner := s.SubAgentRunner(); runner != nil {
			if handle, ok := runner.Manager().GetActive(agent.AgentID(agentID)); ok {
				handle.Cancel()
				return true
			}
		}
		return false
	}
	checkpoint, ok := s.pendingRecoveries[agentID]
	if !ok || checkpoint.Status.ID != recoveryID || checkpoint.Status.Generation != generation {
		s.recoveryMu.Unlock()
		return false
	}
	s.recoveryMu.Unlock()
	s.orchMu.Lock()
	currentOrchestrator := s.orchestrators[agentID]
	s.orchMu.Unlock()
	if currentOrchestrator != nil {
		currentOrchestrator.turnMu.Lock()
		currentGeneration := currentOrchestrator.turnGeneration
		live := currentOrchestrator.turnCancel != nil
		currentOrchestrator.turnMu.Unlock()
		if currentGeneration != generation {
			return false
		}
		if live {
			cancelled, _ := currentOrchestrator.cancelTurnForGeneration(generation)
			return cancelled
		}
	}

	var cancelledChild *agent.SubAgentResult
	cancelledStatus := checkpoint.Status
	cancelledStatus.State = recoverytypes.Cancelled
	cancelledStatus.RetryAt = time.Time{}
	cancelledStatus.Summary = "Run cancelled"
	cancelledStatus.Action = ""
	cancelledStatus.UpdatedAt = time.Now()
	if len(checkpoint.OwnerData) > 0 {
		// Recheck under the shared lifecycle fence before settling parked work.
		s.recoveryMu.Lock()
		current, exists := s.pendingRecoveries[agentID]
		valid := exists && s.recoverySlots[agentID] == nil && current.Status.ID == recoveryID && current.Status.Generation == generation
		s.recoveryMu.Unlock()
		if !valid {
			return false
		}
		checkpoint = current
		var owner childRecoveryOwner
		if json.Unmarshal(checkpoint.OwnerData, &owner) != nil ||
			string(owner.Spec.ID) != agentID {
			return false
		}
		result := agent.SubAgentResult{
			AgentID: owner.Spec.ID, SessionID: agentID, Name: owner.Spec.Name,
			Category: owner.Spec.Category, Status: agent.SubAgentCancelled,
			HistoryMessageID: owner.Spec.HistoryMessageID,
			Errors:           []string{"Cancelled explicitly while recovery was pending."},
		}
		path := s.completedSubAgentDBPath(agentID)
		if path == "" {
			return false
		}
		childDB, err := sessiondb.OpenAndMigrate(path)
		if err != nil {
			return false
		}
		handle := &agent.SubAgentHandle{
			DB: childDB, SessionID: agentID, Spec: owner.Spec,
		}
		checkpoint, err = publishStoppedChildRecovery(handle, checkpoint, cancelledStatus)
		if err == nil {
			err = sessiondb.FinishAgentTurn(childDB, agentID, agentID, generation, sessiondb.TurnInterrupted)
		}
		closeErr := childDB.Close()
		if err != nil || closeErr != nil {
			return false
		}
		if s.db == nil || sessiondb.InsertSubAgentResult(s.db, subAgentResultRow(s.sessionID, result)) != nil {
			return false
		}
		cancelledStatus = checkpoint.Status
		cancelledChild = &result
	} else {
		archive, active := stoppedRecoveryContext(checkpoint)
		current, found, loadErr := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
		if loadErr != nil {
			return false
		}
		if found {
			active = append([]provider.ChatMessage(nil), current.Messages...)
			if len(active) == 0 || active[len(active)-1].EffectiveOrigin() != provider.OriginTurnStopped {
				boundary := provider.NewTextMessage("user", rootprompt.TurnStoppedBoundary())
				boundary.InternalID = "turn-stopped:" + checkpoint.Status.ID
				boundary.Origin, boundary.Visibility = provider.OriginTurnStopped, provider.VisibilityContextOnly
				active = append(active, boundary)
			}
			checkpoint.ContextRevision = current.Revision
		}
		checkpoint.Archive, checkpoint.ActiveContext = archive, active
		checkpoint.Status = cancelledStatus
		checkpoint.ContextOwnerID = s.sessionID
		checkpoint.ContextReason = "explicit_recovery_cancel"
		s.msgsMu.Lock()
		prefixLen := len(s.msgs)
		s.msgsMu.Unlock()
		if err := s.finalizeTurn(
			archive, prefixLen, &displayMetaTracker{metas: make(map[string]*sessiondb.DisplayMeta)},
			"", checkpoint.PendingInput.InternalID,
			ttypes.TurnIdentity{Generation: generation}, active,
		); err != nil {
			return false
		}
		if err := sessiondb.FinishAgentTurn(s.db, s.sessionID, agentID, generation, sessiondb.TurnInterrupted); err != nil {
			return false
		}
	}
	parentCheckpoint := checkpoint
	if len(parentCheckpoint.OwnerData) > 0 {
		parentCheckpoint.ContextOwnerID = ""
	}
	if cancelledChild == nil {
		s.msgsMu.Lock()
		_, err := s.publishRootContext(sessiondb.ContextPublication{RuntimeAgentID: parentCheckpoint.Status.AgentID, Recovery: &parentCheckpoint})
		s.msgsMu.Unlock()
		if err != nil {
			return false
		}
	} else if err := sessiondb.SaveRecoveryCheckpoint(s.db, s.sessionID, parentCheckpoint); err != nil {
		return false
	}
	s.recoveryMu.Lock()
	if current, exists := s.pendingRecoveries[agentID]; exists &&
		current.Status.ID == recoveryID && current.Status.Generation == generation {
		delete(s.pendingRecoveries, agentID)
	}
	s.recoveryMu.Unlock()
	if cancelledChild != nil {
		if runner := s.SubAgentRunner(); runner != nil {
			runner.Manager().HydrateCompleted(*cancelledChild)
		}
		if s.sender != nil {
			s.sender.SendAgentEventWithID("sub_agent_completed", map[string]string{
				"agent_id": agentID, "agent_name": cancelledChild.Name,
				"category": cancelledChild.Category, "status": string(agent.SubAgentCancelled),
			}, agentID)
		}
	}
	if s.sender != nil {
		s.sender.SendAgentEventWithID("recovery", cancelledStatus, agentID)
		s.sender.SendAgentEventWithID("done", map[string]any{
			"content": "Cancelled.", "reason": "cancelled",
			"recovery_id": recoveryID, "generation": generation,
			"cancelled_at": time.Now(),
		}, agentID)
	}
	return true
}

func publishStoppedChildRecovery(handle *agent.SubAgentHandle, checkpoint sessiondb.RecoveryCheckpoint, status recoverytypes.Status) (sessiondb.RecoveryCheckpoint, error) {
	agentID := string(handle.Spec.ID)
	durable, open, err := sessiondb.LoadRecoveryCheckpoint(handle.DB, handle.SessionID, agentID)
	if err != nil {
		return checkpoint, err
	}
	if open {
		if durable.Status.ID != status.ID || durable.Status.Generation != status.Generation || durable.ContextOwnerID != agentID {
			return checkpoint, fmt.Errorf("child recovery incident changed before Stop")
		}
		checkpoint = durable
	} else {
		// The child transaction may have committed before a parent result/mirror
		// failed. Retry only that exact cancelled publication, never a new task.
		records, err := sessiondb.LoadRecoveryRecordsForOwner(handle.DB, handle.SessionID, agentID)
		if err != nil {
			return checkpoint, err
		}
		cancelled := false
		for _, record := range records {
			if record.ID == status.ID && record.Generation == status.Generation && record.State == recoverytypes.Cancelled {
				status, cancelled = record, true
				break
			}
		}
		if !cancelled {
			return checkpoint, fmt.Errorf("child recovery incident is no longer pending")
		}
		var archive, pending []byte
		var admitted bool
		if err := handle.DB.QueryRow(`SELECT archive_json,pending_input_json,input_admitted,owner_data
			FROM agent_recoveries WHERE session_id=? AND agent_id=? AND incident_id=?`,
			handle.SessionID, agentID, status.ID).Scan(&archive, &pending, &admitted, &checkpoint.OwnerData); err != nil {
			return checkpoint, err
		}
		if err := json.Unmarshal(archive, &checkpoint.Archive); err != nil {
			return checkpoint, err
		}
		if err := json.Unmarshal(pending, &checkpoint.PendingInput); err != nil {
			return checkpoint, err
		}
		checkpoint.InputAdmitted = admitted
	}
	current, found, err := sessiondb.LoadContextState(handle.DB, handle.SessionID, agentID)
	if err != nil {
		return checkpoint, err
	}
	if !found || current.Revision < checkpoint.ContextRevision {
		return checkpoint, fmt.Errorf("child current context is unavailable or older than its recovery checkpoint")
	}
	// The current context owns active bytes; the parked archive owns older
	// evidence, including accepted output awaiting its first transcript identity.
	run := newRecoveryRunState(checkpoint.PendingInput, &checkpoint)
	run.active = append([]provider.ChatMessage(nil), current.Messages...)
	if err := reconcileCommittedRecoveryContext(run, current); err != nil {
		return checkpoint, err
	}
	archive, active := run.contexts()
	identities := make(map[string]bool, len(archive))
	for _, message := range archive {
		identities[message.InternalID] = true
	}
	if !checkpoint.InputAdmitted && strings.TrimSpace(checkpoint.PendingInput.Text) != "" {
		if checkpoint.PendingInput.InternalID == "" {
			return checkpoint, fmt.Errorf("pending child assignment has no immutable identity")
		}
		if _, exists := identities[checkpoint.PendingInput.InternalID]; !exists {
			pending := inputMessage(provider.NewTextMessage("user", checkpoint.PendingInput.Text), checkpoint.PendingInput)
			archive, active = append(archive, pending), append(active, pending)
		}
		checkpoint.InputAdmitted = true
	}
	boundaryID := "turn-stopped:" + status.ID
	if len(active) > 0 && active[len(active)-1].EffectiveOrigin() == provider.OriginTurnStopped {
		boundaryID = active[len(active)-1].InternalID
	}
	if _, exists := identities[boundaryID]; !exists {
		boundary := provider.NewTextMessage("user", rootprompt.TurnStoppedBoundary())
		boundary.InternalID = boundaryID
		boundary.Origin, boundary.Visibility = provider.OriginTurnStopped, provider.VisibilityContextOnly
		archive, active = append(archive, boundary), append(active, boundary)
	}
	archive, active, deltas, err := childTerminalPublication(handle, archive, active, status.UpdatedAt)
	if err != nil {
		return checkpoint, err
	}
	checkpoint.Status, checkpoint.Archive, checkpoint.ActiveContext = status, archive, active
	checkpoint.ContextOwnerID = agentID
	state, err := sessiondb.PublishContext(handle.DB, sessiondb.ContextPublication{
		SessionID: handle.SessionID, AgentID: agentID, RuntimeAgentID: agentID, ExpectedRevision: current.Revision,
		Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{
			Generation: current.Generation, Reason: "explicit_recovery_cancel", Messages: active,
		}}, TranscriptAppend: deltas, Recovery: &checkpoint,
	})
	if err != nil {
		return checkpoint, err
	}
	checkpoint.ContextRevision, checkpoint.ContextGeneration, checkpoint.ContextReason = state.Revision, state.Generation, state.Reason
	checkpoint.ActiveContext = append([]provider.ChatMessage(nil), state.Messages...)
	return checkpoint, nil
}

func stoppedRecoveryContext(checkpoint sessiondb.RecoveryCheckpoint) ([]provider.ChatMessage, []provider.ChatMessage) {
	archive := append([]provider.ChatMessage(nil), checkpoint.Archive...)
	active := append([]provider.ChatMessage(nil), checkpoint.ActiveContext...)
	if strings.TrimSpace(checkpoint.PendingInput.Text) != "" {
		pending := inputMessage(provider.NewTextMessage("user", checkpoint.PendingInput.Text), checkpoint.PendingInput)
		pending.InternalID = checkpoint.PendingInput.InternalID
		archive = append(archive, pending)
		active = append(active, pending)
	}
	if len(active) > 0 && active[len(active)-1].EffectiveOrigin() == provider.OriginTurnStopped {
		return archive, active
	}
	boundary := provider.NewTextMessage("user", rootprompt.TurnStoppedBoundary())
	boundary.InternalID = "turn-stopped:" + checkpoint.Status.ID
	boundary.Origin = provider.OriginTurnStopped
	boundary.Visibility = provider.VisibilityContextOnly
	return append(archive, boundary), append(active, boundary)
}
