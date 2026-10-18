package session

import (
	"context"
	"sync"
	"sync/atomic"

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
)

type Orchestrator struct {
	ID             string
	session        *Session
	BlockResolveCh chan ttypes.BlockResponse
	closing        atomic.Bool
	stopCh         chan struct{}
	sender         EventSender
	RuntimeEpoch   uint64
	RunID          string
	RunVersion     uint64
	passiveThrough int64

	turnMu         sync.Mutex
	turnGeneration uint64
	turnCancel     context.CancelFunc
	turnCancelling bool
	turnSealed     bool
	inputCh        chan turnRequest
}

type turnRequest struct {
	sessionTurn int
	input       provider.UserInput
	ctx         context.Context
	generation  uint64
	deliveries  []collaboration.PendingDelivery
	recovery    *sessiondb.RecoveryCheckpoint
	retry       bool
}

func (o *Orchestrator) reserveTurn(input provider.UserInput, deliveries []collaboration.PendingDelivery, recovery *sessiondb.RecoveryCheckpoint) (turnRequest, bool) {
	if o == nil || o.closing.Load() {
		return turnRequest{}, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	o.turnMu.Lock()
	defer o.turnMu.Unlock()
	if o.closing.Load() || o.turnCancel != nil {
		cancel()
		return turnRequest{}, false
	}
	if o.session != nil {
		if o.session.ValidateHistoryAdmission() != nil {
			cancel()
			return turnRequest{}, false
		}
		o.session.recoveryMu.Lock()
		_, unfinished := o.session.pendingRecoveries[o.ID]
		o.session.recoveryMu.Unlock()
		if unfinished || o.session.compactionActive(o.ID) {
			cancel()
			return turnRequest{}, false
		}
	}
	o.turnGeneration++
	o.turnCancel, o.turnCancelling = cancel, false
	o.turnSealed = false
	return turnRequest{input: input, ctx: ctx, generation: o.turnGeneration, deliveries: append([]collaboration.PendingDelivery(nil), deliveries...), recovery: recovery}, true
}

func (o *Orchestrator) enqueueReserved(request turnRequest) bool {
	o.turnMu.Lock()
	defer o.turnMu.Unlock()
	if o.closing.Load() || o.session != nil && atomic.LoadInt32(&o.session.closing) != 0 || o.turnGeneration != request.generation || o.turnCancel == nil || o.turnCancelling || request.ctx.Err() != nil {
		return false
	}
	select {
	case o.inputCh <- request:
		return true
	default:
		return false
	}
}

func (o *Orchestrator) submitRecovery(checkpoint sessiondb.RecoveryCheckpoint) bool {
	if o == nil || o.closing.Load() || checkpoint.Status.Generation == 0 {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	o.turnMu.Lock()
	defer o.turnMu.Unlock()
	if o.closing.Load() || o.turnCancel != nil || checkpoint.Status.Generation < o.turnGeneration {
		cancel()
		return false
	}
	if o.session != nil && o.session.ValidateHistoryAdmission() != nil {
		cancel()
		return false
	}
	o.turnGeneration = checkpoint.Status.Generation
	o.turnCancel = cancel
	o.turnCancelling = false
	o.turnSealed = false
	copy := checkpoint
	request := turnRequest{
		input: cloneUserInput(checkpoint.PendingInput), ctx: ctx,
		generation: checkpoint.Status.Generation, recovery: &copy, retry: true,
	}
	select {
	case o.inputCh <- request:
		return true
	default:
		o.turnCancel = nil
		cancel()
		return false
	}
}

func NewOrchestrator(id string, s *Session) *Orchestrator {
	var generation uint64
	if s != nil {
		generation = s.recoveredTurnGenerations[id]
		s.usageMu.RLock()
		for _, record := range s.usageRecords {
			if record.AgentID == id {
				generation = max(generation, record.TurnGeneration)
			}
		}
		s.usageMu.RUnlock()
	}
	return &Orchestrator{
		ID:             id,
		turnGeneration: generation,
		session:        s,
		BlockResolveCh: make(chan ttypes.BlockResponse, 8),
		stopCh:         make(chan struct{}),
		inputCh:        make(chan turnRequest, 1),
	}
}

// SubmitInput creates and registers the turn context before publishing work to
// the session loop. Cancellation is therefore reliable even in the tiny
// admission-to-receive window that channel-only signalling could not cover.
func (o *Orchestrator) SubmitInput(input provider.UserInput) bool {
	if o != nil && o.session != nil {
		input = o.session.bindInputExecution(input)
	}
	return o.submitTurn(input, nil, nil)
}

func (o *Orchestrator) SubmitCollaboration(input provider.UserInput, deliveries []collaboration.PendingDelivery) bool {
	if len(deliveries) == 0 {
		return false
	}
	return o.submitTurn(input, deliveries, nil)
}

func (o *Orchestrator) submitTurn(input provider.UserInput, deliveries []collaboration.PendingDelivery, recovery *sessiondb.RecoveryCheckpoint) bool {
	request, ok := o.reserveTurn(input, deliveries, recovery)
	if !ok {
		return false
	}
	if o.enqueueReserved(request) {
		return true
	}
	o.endTurn(request.generation)
	return false
}

func (o *Orchestrator) endTurn(generation uint64) []string {
	o.turnMu.Lock()
	if generation != o.turnGeneration {
		o.turnMu.Unlock()
		return nil
	}
	cancel := o.turnCancel
	o.turnCancel = nil
	o.turnCancelling = false
	o.turnSealed = false
	o.RunID = ""
	o.RunVersion = 0
	o.turnMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// CancelTurn synchronously marks the current generation as cancelling before
// invoking its context cancel function. A later turn can never observe it.
func (o *Orchestrator) CancelTurn() (bool, []string) {
	return o.cancelTurnForGeneration(0)
}

// Zero selects the current turn for an ordinary immediate Stop. Recovery
// actions supply their captured generation so a replaced context is never hit.
func (o *Orchestrator) cancelTurnForGeneration(expected uint64) (bool, []string) {
	if o == nil {
		return false, nil
	}
	o.turnMu.Lock()
	if o.turnCancel == nil || expected != 0 && o.turnGeneration != expected {
		o.turnMu.Unlock()
		return false, nil
	}
	if o.turnCancelling {
		o.turnMu.Unlock()
		return true, nil
	}
	o.turnCancelling = true
	cancel := o.turnCancel
	generation := o.turnGeneration
	o.turnMu.Unlock()
	cancel()
	if runner := o.subAgentRunner(); runner != nil {
		runner.CancelActiveForTurn(generation)
	}
	return true, nil
}

func (o *Orchestrator) cancelCollaborationRun(runID string, expectedVersion, appliedVersion uint64) error {
	o.turnMu.Lock()
	if o.RunID != runID || o.RunVersion != expectedVersion || o.turnCancel == nil {
		o.turnMu.Unlock()
		return collaboration.ErrStale
	}
	o.RunVersion = appliedVersion
	o.turnCancelling = true
	cancel, generation := o.turnCancel, o.turnGeneration
	o.turnMu.Unlock()
	cancel()
	if runner := o.subAgentRunner(); runner != nil {
		runner.CancelActiveForTurn(generation)
	}
	return nil
}

func (o *Orchestrator) ResolveBlock(response ttypes.BlockResponse) bool {
	if o == nil || response.RequestID == "" || o.closing.Load() {
		return false
	}
	select {
	case o.BlockResolveCh <- response:
		return true
	default:
		return false
	}
}

func (o *Orchestrator) TurnActive() bool {
	if o == nil {
		return false
	}
	o.turnMu.Lock()
	defer o.turnMu.Unlock()
	return o.turnCancel != nil
}

func (o *Orchestrator) setCollaborationRun(run collaboration.Run) {
	o.turnMu.Lock()
	o.RunID, o.RunVersion = run.ID, run.Version
	o.turnMu.Unlock()
}

func (o *Orchestrator) collaborationRunIdentity() (string, uint64) {
	o.turnMu.Lock()
	defer o.turnMu.Unlock()
	return o.RunID, o.RunVersion
}

// AgentTurnGeneration includes interrupted and unbilled turns and is restored from
// durable runtime state. Usage request counts are not the session turn clock.
func (s *Session) AgentTurnGeneration(id string) uint64 {
	if s == nil {
		return 0
	}
	s.orchMu.Lock()
	o := s.orchestrators[id]
	s.orchMu.Unlock()
	if o == nil {
		return s.recoveredTurnGenerations[id]
	}
	o.turnMu.Lock()
	defer o.turnMu.Unlock()
	return o.turnGeneration
}

func (o *Orchestrator) subAgentRunner() *SubAgentRunner {
	if o == nil || o.session == nil {
		return nil
	}
	return o.session.SubAgentRunner()
}
