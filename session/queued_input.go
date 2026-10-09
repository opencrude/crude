package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"crude/provider"
	"crude/sessiondb"
)

// InputAdmissionQueued means the input was durably queued because its owner
// was compacting (or earlier input is still queued). It runs, in order, when
// compaction finishes or the preceding turn completes naturally.
const InputAdmissionQueued InputAdmissionStatus = "queued"

const queuedInputsStateKey = "queued_inputs"

// QueuedInput is one durable human input awaiting admission. The queue
// belongs to the session's root, so it survives orchestrator replacement.
type QueuedInput struct {
	Input         provider.UserInput `json:"input"`
	DraftRevision uint64             `json:"draft_revision"`
	QueuedAt      time.Time          `json:"queued_at"`
}

// QueuedInputs returns the durable queue in admission order.
func (s *Session) QueuedInputs() []QueuedInput {
	if s == nil {
		return nil
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	out := make([]QueuedInput, len(s.queued))
	for i, item := range s.queued {
		out[i] = QueuedInput{Input: cloneUserInput(item.Input), DraftRevision: item.DraftRevision, QueuedAt: item.QueuedAt}
	}
	return out
}

// loadQueuedInputs runs during off-thread creation/resume, never UI admission
// or hydration. Corrupt/unreadable owner state must not be mistaken for empty.
func (s *Session) loadQueuedInputs() error {
	raw, err := sessiondb.LoadRuntimeState(context.Background(), s.db, queuedInputsStateKey)
	if err != nil {
		return err
	}
	var items []QueuedInput
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &items); err != nil {
			return fmt.Errorf("restore queued input: %w", err)
		}
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if item.Input.InternalID == "" || seen[item.Input.InternalID] || item.QueuedAt.IsZero() || strings.TrimSpace(item.Input.Text) == "" {
			return errors.New("restore queued input: invalid or duplicate input identity")
		}
		seen[item.Input.InternalID] = true
	}
	s.queueMu.Lock()
	s.queued = items
	s.queueMu.Unlock()
	return nil
}

func (s *Session) persistQueuedInputsLocked(items []QueuedInput) error {
	if s.db == nil {
		return errors.New("session database is unavailable")
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return sessiondb.SaveRuntimeState(context.Background(), s.db, queuedInputsStateKey, raw)
}

// shouldQueueInput reports whether new input must wait: its owner is
// compacting, or earlier input is still queued and order must be kept.
func (s *Session) shouldQueueInput(agentID string) bool {
	if s.compactionActive(agentID) {
		return true
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return len(s.queued) != 0
}

// queueInput durably appends one input. Nothing is acknowledged before the
// queue is persisted, so a crash can never lose an acknowledged prompt.
func (s *Session) queueInput(agentID string, input provider.UserInput, draftRevision uint64) (InputAdmissionReceipt, error) {
	s.queueMu.Lock()
	item := QueuedInput{Input: cloneUserInput(input), DraftRevision: draftRevision, QueuedAt: time.Now()}
	next := append(append([]QueuedInput(nil), s.queued...), item)
	if err := s.persistQueuedInputsLocked(next); err != nil {
		s.queueMu.Unlock()
		return InputAdmissionReceipt{}, err
	}
	s.queued = next
	s.queueMu.Unlock()
	if sender := s.orchestratorSender(agentID); sender != nil {
		sender.SendAgentEventWithID("input_queued", map[string]any{
			"input_id": input.InternalID, "text": input.Text, "display_text": input.DisplayText,
		}, agentID)
	}
	// Order is preserved, but an idle owner should not wait for a trigger
	// that will never come (for example after a passive resume).
	go s.AdmitQueuedInputs(agentID)
	return InputAdmissionReceipt{SessionID: s.sessionID, AgentID: agentID, InputID: input.InternalID,
		DraftRevision: draftRevision, AcceptedAt: item.QueuedAt}, nil
}

// AdmitQueuedInputs admits the head of the queue when its owner is idle and
// not compacting. Each natural turn completion admits the next one.
func (s *Session) AdmitQueuedInputs(agentID string) {
	if s == nil {
		return
	}
	s.queueAdmitMu.Lock()
	defer s.queueAdmitMu.Unlock()
	if atomic.LoadInt32(&s.closing) != 0 || s.compactionActive(agentID) {
		return
	}
	s.queueMu.Lock()
	if len(s.queued) == 0 {
		s.queueMu.Unlock()
		return
	}
	head := s.queued[0]
	s.queueMu.Unlock()

	intent, err := s.prepareReservedAdmission(agentID, head.Input, head.DraftRevision)
	if err != nil {
		// Busy or unfinished work: a later completion or compaction retries.
		return
	}
	intent.dequeue = true
	s.CommitInputAdmission(context.Background(), intent)
}

// publishQueuedInput consumes the exact head atomically with its accepted
// context and recovery checkpoint. Failure starts no turn and preserves the
// queue; a crash after commit cannot replay an already-admitted head.
// Caller holds msgsMu; queueMu serializes appends with this transaction.
func (s *Session) publishQueuedInput(intent *InputAdmissionIntent, publication sessiondb.ContextPublication) (sessiondb.ContextState, error) {
	if !intent.dequeue {
		return s.publishRootContext(publication)
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if len(s.queued) == 0 || s.queued[0].Input.InternalID != intent.InputID {
		return sessiondb.ContextState{}, errors.New("queued input head changed before admission")
	}
	rest := append([]QueuedInput(nil), s.queued[1:]...)
	raw, err := json.Marshal(rest)
	if err != nil {
		return sessiondb.ContextState{}, err
	}
	publication.RuntimeState = &sessiondb.RuntimeStateUpdate{Key: queuedInputsStateKey, Value: raw}
	state, err := s.publishRootContext(publication)
	if err == nil {
		s.queued = rest
	}
	return state, err
}

func (s *Session) orchestratorSender(agentID string) EventSender {
	s.orchMu.Lock()
	defer s.orchMu.Unlock()
	if o := s.orchestrators[agentID]; o != nil {
		return o.sender
	}
	return nil
}

var errQueueOwnerUnavailable = errors.New("input owner is unavailable")
