package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"crude/provider"
	"crude/sessiondb"
)

type InputAdmissionStatus string

const (
	InputAdmissionAccepted            InputAdmissionStatus = "accepted"
	InputAdmissionRejected            InputAdmissionStatus = "rejected"
	InputAdmissionPreservationPending InputAdmissionStatus = "preservation_pending"
)

type InputAdmissionErrorKind string

const (
	InputAdmissionPreLocalRejection InputAdmissionErrorKind = "pre_local_rejection"
	InputAdmissionStorageFailure    InputAdmissionErrorKind = "storage"
	InputAdmissionDispatchFailure   InputAdmissionErrorKind = "dispatch"
)

type InputAdmissionReceipt struct {
	SessionID       string
	AgentID         string
	InputID         string
	ContextRevision uint64
	TurnGeneration  uint64
	OwnerEpoch      uint64
	DraftRevision   uint64
	AcceptedAt      time.Time
}

type InputAdmissionResult struct {
	Status    InputAdmissionStatus
	Receipt   InputAdmissionReceipt
	ErrorKind InputAdmissionErrorKind
	Err       error
}

func (r InputAdmissionResult) Accepted() bool { return r.Status == InputAdmissionAccepted }

// InputAdmissionIntent is an immutable, owner-fenced reservation prepared on
// the caller thread without persistence or provider work. Commit is serialized
// and idempotent on the intent itself.
type InputAdmissionIntent struct {
	SessionID      string
	AgentID        string
	InputID        string
	DraftRevision  uint64
	TurnGeneration uint64
	OwnerEpoch     uint64

	session      *Session
	orchestrator *Orchestrator
	request      turnRequest
	mu           sync.Mutex
	committed    bool
	result       InputAdmissionResult
}

// PrepareInputAdmission reserves the exact owner generation synchronously but
// performs no database, filesystem, provider, reference, or command I/O.
func (s *Session) PrepareInputAdmission(orchestratorID string, input provider.UserInput, draftRevision uint64) (*InputAdmissionIntent, error) {
	if s == nil {
		return nil, errors.New("session is unavailable")
	}
	s.orchMu.Lock()
	orchestrator := s.orchestrators[orchestratorID]
	s.orchMu.Unlock()
	if orchestrator == nil || orchestrator.closing.Load() {
		return nil, errors.New("input owner is unavailable")
	}
	input = cloneUserInput(input)
	if input.InternalID == "" {
		input.InternalID = newMessageID()
	}
	request, reserved := orchestrator.reserveTurn(input, nil, nil)
	if !reserved {
		return nil, errors.New("input owner is busy or has unfinished work")
	}
	intent := &InputAdmissionIntent{
		SessionID: s.sessionID, AgentID: orchestratorID, InputID: input.InternalID,
		DraftRevision: draftRevision, TurnGeneration: request.generation, OwnerEpoch: orchestrator.RuntimeEpoch,
		session: s, orchestrator: orchestrator, request: request,
	}
	s.admissionMu.Lock()
	if s.pendingAdmissions == nil {
		s.pendingAdmissions = make(map[*InputAdmissionIntent]struct{})
	}
	s.pendingAdmissions[intent] = struct{}{}
	s.admissionMu.Unlock()
	return intent, nil
}

// CommitInputAdmission durably accepts one prepared immutable input before it
// is queued for execution. It is intended to run in an off-UI command. Stop may
// cancel the reserved generation before/during this call; the local commit still
// succeeds exactly once and returns preservation_pending without starting work.
func (s *Session) CommitInputAdmission(ctx context.Context, intent *InputAdmissionIntent) InputAdmissionResult {
	if s == nil || intent == nil || intent.session != s || intent.orchestrator == nil ||
		intent.SessionID != s.sessionID || intent.AgentID != intent.orchestrator.ID ||
		intent.InputID == "" || intent.TurnGeneration == 0 {
		return InputAdmissionResult{Status: InputAdmissionRejected, ErrorKind: InputAdmissionPreLocalRejection, Err: errors.New("input admission intent is invalid")}
	}
	intent.mu.Lock()
	defer intent.mu.Unlock()
	if intent.committed {
		return intent.result
	}
	defer func() {
		s.admissionMu.Lock()
		delete(s.pendingAdmissions, intent)
		s.admissionMu.Unlock()
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	// Context cancellation stops execution, not ownership of an already
	// prepared local send. Persistence uses the session lifetime below.
	orchestrator, request := intent.orchestrator, intent.request
	input := cloneUserInput(request.input)
	model := ""
	if s.runtimeProfile != nil {
		if cfg := s.runtimeProfile.GetEffectiveConfig(intent.AgentID); cfg != nil {
			model = cfg.GetActiveDefaultCategory().Model
		}
	}
	message, labels := s.buildUserMessageWithDisplay(input, model)
	message.InternalID = input.InternalID
	message.DisplayContent = input.Text
	if input.DisplayText != "" {
		message.DisplayContent = input.DisplayText
	}
	createdAt := time.Now()
	meta := canonicalizeDisplayMeta(message, sessiondb.DisplayMeta{DisplayRole: "user", DisplayContent: input.Text, ReferenceLabels: labels}, "", 0, createdAt)
	s.msgsMu.Lock()
	state, err := s.publishRootContext(sessiondb.ContextPublication{Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{
		contextDelta(message, meta, s.turnNumber, createdAt, intent.AgentID),
	}}})
	if err == nil {
		s.msgs = append(s.msgs, message)
		s.msgTurns = append(s.msgTurns, s.turnNumber)
		s.displayMetas = append(s.displayMetas, meta)
		s.createdAts = append(s.createdAts, createdAt.UnixNano())
	}
	archive := append([]provider.ChatMessage(nil), s.msgs...)
	s.msgsMu.Unlock()
	if err != nil {
		orchestrator.endTurn(request.generation)
		intent.result = InputAdmissionResult{Status: InputAdmissionRejected, ErrorKind: InputAdmissionStorageFailure, Err: err}
		intent.committed = true
		return intent.result
	}
	receipt := InputAdmissionReceipt{SessionID: s.sessionID, AgentID: intent.AgentID, InputID: input.InternalID, ContextRevision: state.Revision, TurnGeneration: request.generation, OwnerEpoch: intent.OwnerEpoch, DraftRevision: intent.DraftRevision, AcceptedAt: createdAt}
	checkpoint := sessiondb.RecoveryCheckpoint{PendingInput: cloneUserInput(input), InputAdmitted: true, Archive: archive, ActiveContext: append([]provider.ChatMessage(nil), state.Messages...), ContextOwnerID: s.sessionID, ContextRevision: state.Revision, ContextGeneration: state.Generation, ContextReason: state.Reason}
	request.recovery = &checkpoint
	if !orchestrator.enqueueReserved(request) {
		orchestrator.endTurn(request.generation)
		intent.result = InputAdmissionResult{Status: InputAdmissionPreservationPending, Receipt: receipt, ErrorKind: InputAdmissionDispatchFailure, Err: errors.New("input was saved to context but execution is unavailable")}
		intent.committed = true
		return intent.result
	}
	intent.result = InputAdmissionResult{Status: InputAdmissionAccepted, Receipt: receipt}
	intent.committed = true
	return intent.result
}
