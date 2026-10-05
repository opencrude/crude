package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"crude/agent"
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
	mu                sync.Mutex
	pending           provider.UserInput
	inputAdmitted     bool
	archive           []provider.ChatMessage
	active            []provider.ChatMessage
	contextOwnerID    string
	contextGeneration uint64
	contextReason     string
	ownerData         []byte
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
	state.contextGeneration = checkpoint.ContextGeneration
	state.contextReason = checkpoint.ContextReason
	state.ownerData = append([]byte(nil), checkpoint.OwnerData...)
	return state
}

func (s *recoveryRunState) snapshot(archive, active []provider.ChatMessage) {
	s.mu.Lock()
	s.archive = append([]provider.ChatMessage(nil), archive...)
	s.active = append([]provider.ChatMessage(nil), active...)
	s.mu.Unlock()
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

func (s *recoveryRunState) setContext(owner string, generation uint64, reason string) {
	s.mu.Lock()
	s.contextOwnerID = owner
	s.contextGeneration = max(uint64(1), generation)
	if reason != "" {
		s.contextReason = reason
	}
	s.mu.Unlock()
}

func (s *recoveryRunState) appendContinuation(kind rootprompt.RecoveryKind) {
	message := provider.NewTextMessage("user", rootprompt.RecoveryContinuation(kind))
	message.Origin = provider.OriginContinuation
	message.Visibility = provider.VisibilityContextOnly
	s.mu.Lock()
	s.archive = append(s.archive, message)
	s.active = append(s.active, message)
	s.mu.Unlock()
}

func (s *recoveryRunState) contexts() (archive, active []provider.ChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]provider.ChatMessage(nil), s.archive...),
		append([]provider.ChatMessage(nil), s.active...)
}

func (s *recoveryRunState) checkpoint(status recoverytypes.Status) sessiondb.RecoveryCheckpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sessiondb.RecoveryCheckpoint{
		Status: status, PendingInput: cloneUserInput(s.pending), InputAdmitted: s.inputAdmitted,
		Archive:        append([]provider.ChatMessage(nil), s.archive...),
		ActiveContext:  append([]provider.ChatMessage(nil), s.active...),
		ContextOwnerID: s.contextOwnerID, ContextGeneration: s.contextGeneration,
		ContextReason: s.contextReason, OwnerData: append([]byte(nil), s.ownerData...),
	}
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
		return sessiondb.SaveRecoveryCheckpoint(db, sessionID, state.checkpoint(status))
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

func (s *Session) persistPendingRecovery(
	db *sql.DB,
	sessionID string,
	supervisor *agent.Supervisor,
	state *recoveryRunState,
) error {
	if supervisor == nil {
		return nil
	}
	return sessiondb.SaveRecoveryCheckpoint(db, sessionID, state.checkpoint(supervisor.CheckpointStatus()))
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
			cancelled, _ := orchestrator.CancelTurn()
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
		currentOrchestrator.turnMu.Unlock()
		if currentGeneration != generation {
			return false
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
		archive, active := stoppedRecoveryContext(checkpoint)
		checkpoint.Archive, checkpoint.ActiveContext = archive, active
		checkpoint.Status = cancelledStatus
		checkpoint.ContextOwnerID = agentID
		checkpoint.ContextReason = "explicit_recovery_cancel"
		handle := &agent.SubAgentHandle{
			DB: childDB, SessionID: agentID, Spec: owner.Spec,
		}
		err = persistSubAgentTranscript(handle, archive)
		if err == nil {
			err = sessiondb.SaveContextState(childDB, agentID, agentID, sessiondb.ContextState{
				AgentID: agentID, Generation: max(uint64(1), checkpoint.ContextGeneration),
				Reason: "explicit_recovery_cancel", Messages: active,
			})
		}
		if err == nil {
			err = sessiondb.FinishAgentTurn(childDB, agentID, agentID, generation, sessiondb.TurnInterrupted)
		}
		if err == nil {
			err = sessiondb.SaveRecoveryCheckpoint(childDB, agentID, checkpoint)
		}
		closeErr := childDB.Close()
		if err != nil || closeErr != nil {
			return false
		}
		if s.db == nil || sessiondb.InsertSubAgentResult(s.db, subAgentResultRow(s.sessionID, result)) != nil {
			return false
		}
		cancelledChild = &result
	} else {
		archive, active := stoppedRecoveryContext(checkpoint)
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
	if err := sessiondb.SaveRecoveryCheckpoint(s.db, s.sessionID, parentCheckpoint); err != nil {
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

func stoppedRecoveryContext(checkpoint sessiondb.RecoveryCheckpoint) ([]provider.ChatMessage, []provider.ChatMessage) {
	archive := append([]provider.ChatMessage(nil), checkpoint.Archive...)
	active := append([]provider.ChatMessage(nil), checkpoint.ActiveContext...)
	if len(active) > 0 && active[len(active)-1].EffectiveOrigin() == provider.OriginTurnStopped {
		return archive, active
	}
	boundary := provider.NewTextMessage("user", rootprompt.TurnStoppedBoundary())
	boundary.InternalID = "turn-stopped:" + checkpoint.Status.ID
	boundary.Origin = provider.OriginTurnStopped
	boundary.Visibility = provider.VisibilityContextOnly
	return append(archive, boundary), append(active, boundary)
}
