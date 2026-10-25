package session

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/sessiondb"
)

type InputAdmissionStatus string

const (
	InputAdmissionAccepted            InputAdmissionStatus = "accepted"
	InputAdmissionRejected            InputAdmissionStatus = "rejected"
	InputAdmissionPreservationPending InputAdmissionStatus = "preservation_pending"
	// InputAdmissionSteered binds the input to a durable event on one live run.
	// Context admission follows at a safe boundary; the journal receipt owns
	// acceptance in the meantime.
	InputAdmissionSteered InputAdmissionStatus = "steered"
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
	EventID         string
	RunID           string
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
	// queued marks input bound for the durable queue rather than a turn.
	queued    bool
	dequeue   bool
	mu        sync.Mutex
	committed bool
	result    InputAdmissionResult
}

// PrepareInputAdmission reserves the exact owner generation synchronously but
// performs no database, filesystem, provider, reference, or command I/O. Input
// for a compacting owner, or behind already queued input, is marked for the
// durable queue instead; Commit persists it.
func (s *Session) PrepareInputAdmission(orchestratorID string, input provider.UserInput, draftRevision uint64) (*InputAdmissionIntent, error) {
	if s == nil {
		return nil, errors.New("session is unavailable")
	}
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	if atomic.LoadInt32(&s.closing) != 0 {
		return nil, errors.New("session is closing")
	}
	if err := s.ValidateHistoryAdmission(); err != nil {
		return nil, err
	}
	input = s.bindInputExecution(input)
	if s.shouldQueueInput(orchestratorID) {
		s.orchMu.Lock()
		orchestrator := s.orchestrators[orchestratorID]
		s.orchMu.Unlock()
		if orchestrator == nil || orchestrator.closing.Load() || atomic.LoadInt32(&s.closing) != 0 {
			return nil, errQueueOwnerUnavailable
		}
		input = cloneUserInput(input)
		if input.InternalID == "" {
			input.InternalID = newMessageID()
		}
		intent := &InputAdmissionIntent{SessionID: s.sessionID, AgentID: orchestratorID, InputID: input.InternalID,
			DraftRevision: draftRevision, OwnerEpoch: orchestrator.RuntimeEpoch, session: s, orchestrator: orchestrator,
			request: turnRequest{input: input}, queued: true}
		s.registerInputAdmission(intent)
		return intent, nil
	}
	return s.prepareReservedAdmissionLocked(orchestratorID, input, draftRevision)
}

// prepareReservedAdmission reserves a turn generation for immediate admission.
func (s *Session) prepareReservedAdmission(orchestratorID string, input provider.UserInput, draftRevision uint64) (*InputAdmissionIntent, error) {
	if s == nil {
		return nil, errors.New("session is unavailable")
	}
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	return s.prepareReservedAdmissionLocked(orchestratorID, input, draftRevision)
}

func (s *Session) prepareReservedAdmissionLocked(orchestratorID string, input provider.UserInput, draftRevision uint64) (*InputAdmissionIntent, error) {
	if atomic.LoadInt32(&s.closing) != 0 {
		return nil, errors.New("session is closing")
	}
	if err := s.ValidateHistoryAdmission(); err != nil {
		return nil, err
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
	s.msgsMu.Lock()
	request.sessionTurn = s.turnNumber + 1
	s.msgsMu.Unlock()
	intent := &InputAdmissionIntent{
		SessionID: s.sessionID, AgentID: orchestratorID, InputID: input.InternalID,
		DraftRevision: draftRevision, TurnGeneration: request.generation, OwnerEpoch: orchestrator.RuntimeEpoch,
		session: s, orchestrator: orchestrator, request: request,
	}
	s.registerInputAdmission(intent)
	return intent, nil
}

func (s *Session) registerInputAdmission(intent *InputAdmissionIntent) {
	if s.pendingAdmissions == nil {
		s.pendingAdmissions = make(map[*InputAdmissionIntent]struct{})
	}
	s.pendingAdmissions[intent] = struct{}{}
}

func (s *Session) finishInputAdmission(intent *InputAdmissionIntent) {
	s.admissionMu.Lock()
	delete(s.pendingAdmissions, intent)
	s.admissionMu.Unlock()
}

// CommitInputAdmission durably accepts one prepared immutable input before it
// is queued for execution. It is intended to run in an off-UI command. Stop may
// cancel the reserved generation before/during this call; the local commit still
// succeeds exactly once and returns preservation_pending without starting work.
func (s *Session) CommitInputAdmission(ctx context.Context, intent *InputAdmissionIntent) InputAdmissionResult {
	if intent != nil && intent.queued && intent.session == s && s != nil {
		intent.mu.Lock()
		defer intent.mu.Unlock()
		if intent.committed {
			return intent.result
		}
		if intent.orchestrator == nil || intent.SessionID != s.sessionID || intent.AgentID != intent.orchestrator.ID ||
			intent.InputID == "" || intent.InputID != intent.request.input.InternalID || intent.OwnerEpoch != intent.orchestrator.RuntimeEpoch {
			return InputAdmissionResult{Status: InputAdmissionRejected, ErrorKind: InputAdmissionPreLocalRejection, Err: errors.New("queued input admission intent is invalid")}
		}
		defer s.finishInputAdmission(intent)
		unlock, err := s.lockInputExecution(intent.request.input)
		if err != nil {
			intent.committed = true
			intent.result = InputAdmissionResult{Status: InputAdmissionRejected, ErrorKind: InputAdmissionPreLocalRejection, Err: err}
			return intent.result
		}
		defer unlock()
		receipt, err := s.queueInput(intent.AgentID, intent.request.input, intent.DraftRevision)
		intent.committed = true
		if err != nil {
			intent.result = InputAdmissionResult{Status: InputAdmissionRejected, ErrorKind: InputAdmissionStorageFailure, Err: err}
			return intent.result
		}
		receipt.OwnerEpoch = intent.OwnerEpoch
		intent.result = InputAdmissionResult{Status: InputAdmissionQueued, Receipt: receipt}
		return intent.result
	}
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
	defer s.finishInputAdmission(intent)
	unlock, executionErr := s.lockInputExecution(intent.request.input)
	if executionErr != nil {
		intent.orchestrator.endTurn(intent.TurnGeneration)
		intent.committed = true
		intent.result = InputAdmissionResult{Status: InputAdmissionRejected, ErrorKind: InputAdmissionPreLocalRejection, Err: executionErr}
		return intent.result
	}
	defer unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	// Context cancellation stops execution, not ownership of an already
	// prepared local send. Persistence uses the session lifetime below.
	orchestrator, request := intent.orchestrator, intent.request
	input := cloneUserInput(request.input)
	var cfg *Config
	if s.runtimeProfile != nil {
		cfg = s.runtimeProfile.GetEffectiveConfig(intent.AgentID)
	}
	prepared, _ := s.prepareInput(context.WithoutCancel(ctx), input, cfg, orchestrator)
	message := prepared.Message
	message.InternalID = input.InternalID
	message.DisplayContent = input.Text
	if input.DisplayText != "" {
		message.DisplayContent = input.DisplayText
	}
	createdAt := time.Now()
	meta := canonicalizeDisplayMeta(message, sessiondb.DisplayMeta{DisplayRole: "user", DisplayContent: input.Text, ReferenceLabels: prepared.ReferenceLabels, Attachments: prepared.Attachments, TurnGeneration: request.generation}, "", 0, createdAt)
	s.msgsMu.Lock()
	turn := request.sessionTurn
	deltas := []sessiondb.ContextMessageDelta{contextDelta(message, meta, turn, createdAt, intent.AgentID)}
	for _, enrichment := range prepared.Enrichment {
		meta := canonicalizeDisplayMeta(enrichment, sessiondb.DisplayMeta{DisplayRole: "hidden_runtime_context", ContextOnly: true}, "", 0, createdAt)
		deltas = append(deltas, contextDelta(enrichment, meta, turn, createdAt, intent.AgentID))
	}
	checkpoint := sessiondb.RecoveryCheckpoint{
		Status:       agent.NewSupervisor(intent.AgentID, request.generation, nil).CheckpointStatus(),
		PendingInput: cloneUserInput(input), InputAdmitted: true,
		Archive:       append([]provider.ChatMessage(nil), s.msgs...),
		ActiveContext: append([]provider.ChatMessage(nil), s.activeContext...),
	}
	for _, delta := range deltas {
		checkpoint.Archive = append(checkpoint.Archive, delta.Message)
		checkpoint.ActiveContext = append(checkpoint.ActiveContext, delta.Message)
	}
	state, err := s.publishQueuedInput(intent, sessiondb.ContextPublication{RuntimeAgentID: intent.AgentID, Recovery: &checkpoint, Mutation: sessiondb.ContextMutation{Append: deltas}})
	if err == nil {
		s.turnNumber = max(s.turnNumber, turn)
		for _, delta := range deltas {
			s.msgs = append(s.msgs, delta.Message)
			s.msgTurns = append(s.msgTurns, delta.Turn)
			s.displayMetas = append(s.displayMetas, delta.Meta)
			s.createdAts = append(s.createdAts, delta.CreatedAt)
		}
	}
	s.msgsMu.Unlock()
	if err != nil {
		orchestrator.endTurn(request.generation)
		intent.result = InputAdmissionResult{Status: InputAdmissionRejected, ErrorKind: InputAdmissionStorageFailure, Err: err}
		intent.committed = true
		return intent.result
	}
	notifyAttachmentIssues(s.orchestratorSender(intent.AgentID), intent.AgentID, prepared.AttachmentIssues)
	receipt := InputAdmissionReceipt{SessionID: s.sessionID, AgentID: intent.AgentID, InputID: input.InternalID, ContextRevision: state.Revision, TurnGeneration: request.generation, OwnerEpoch: intent.OwnerEpoch, DraftRevision: intent.DraftRevision, AcceptedAt: createdAt}
	checkpoint.ContextOwnerID, checkpoint.ContextRevision, checkpoint.ContextGeneration, checkpoint.ContextReason = s.sessionID, state.Revision, state.Generation, state.Reason
	checkpoint.ActiveContext = append([]provider.ChatMessage(nil), state.Messages...)
	request.recovery = &checkpoint
	if intent.dequeue {
		// Publish settlement before provider work can stream or finish. The
		// exact row stays chronological even with another pending prompt behind it.
		if sender := s.orchestratorSender(intent.AgentID); sender != nil {
			sender.SendAgentEventWithID("queued_input_admitted", map[string]any{"input_id": intent.InputID}, intent.AgentID)
		}
	}
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
