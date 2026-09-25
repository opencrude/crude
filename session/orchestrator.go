package session

import (
	"context"
	"sync"
	"sync/atomic"

	"crude/provider"
	ttypes "crude/types"
)

type Orchestrator struct {
	ID             string
	session        *Session
	BlockResolveCh chan ttypes.BlockResponse
	closing        atomic.Bool
	stopCh         chan struct{}
	sender         EventSender

	turnMu          sync.Mutex
	turnGeneration  uint64
	turnCancel      context.CancelFunc
	turnCancelling  bool
	inputCh         chan turnRequest
	btwCh           chan provider.UserInput
	pendingBtw      map[string]struct{}
	pendingBtwOrder []string
}

type turnRequest struct {
	input      provider.UserInput
	ctx        context.Context
	generation uint64
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
		btwCh:          make(chan provider.UserInput, 8),
		pendingBtw:     make(map[string]struct{}),
	}
}

// SubmitInput creates and registers the turn context before publishing work to
// the session loop. Cancellation is therefore reliable even in the tiny
// admission-to-receive window that channel-only signalling could not cover.
func (o *Orchestrator) SubmitInput(input provider.UserInput) bool {
	if o == nil || o.closing.Load() {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	o.turnMu.Lock()
	defer o.turnMu.Unlock()
	if o.closing.Load() || o.turnCancel != nil {
		cancel()
		return false
	}
	o.drainBtwLocked()
	o.turnGeneration++
	o.turnCancel = cancel
	o.turnCancelling = false
	request := turnRequest{input: input, ctx: ctx, generation: o.turnGeneration}
	select {
	case o.inputCh <- request:
		return true
	default:
		o.turnCancel = nil
		cancel()
		return false
	}
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
	o.drainBtwLocked()
	cleared := o.clearPendingBtwLocked()
	o.turnMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return cleared
}

// CancelTurn synchronously marks the current generation as cancelling before
// invoking its context cancel function. A later turn can never observe this
// request, and BTW admission closes atomically with cancellation.
func (o *Orchestrator) CancelTurn() (bool, []string) {
	if o == nil {
		return false, nil
	}
	o.turnMu.Lock()
	if o.turnCancel == nil {
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
	o.drainBtwLocked()
	cleared := o.clearPendingBtwLocked()
	o.turnMu.Unlock()
	cancel()
	if runner := o.subAgentRunner(); runner != nil {
		for _, id := range runner.CancelActiveForTurn(generation) {
			if o.session != nil {
				o.session.MarkReportConsumed(string(id))
			}
		}
	}
	return true, cleared
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

// SubmitBtw admits a note only into the currently active, non-cancelling turn.
// The send and lifecycle check share one lock, so a note cannot cross a cancel
// boundary and leak into the next turn.
func (o *Orchestrator) SubmitBtw(input provider.UserInput) bool {
	if o == nil {
		return false
	}
	o.turnMu.Lock()
	defer o.turnMu.Unlock()
	if o.closing.Load() || o.turnCancel == nil || o.turnCancelling || input.InternalID == "" {
		return false
	}
	if o.pendingBtw == nil {
		o.pendingBtw = make(map[string]struct{})
	}
	if _, exists := o.pendingBtw[input.InternalID]; exists {
		return false
	}
	select {
	case o.btwCh <- input:
		o.pendingBtw[input.InternalID] = struct{}{}
		o.pendingBtwOrder = append(o.pendingBtwOrder, input.InternalID)
		return true
	default:
		return false
	}
}

func (o *Orchestrator) consumeBtw(id string) bool {
	if o == nil || id == "" {
		return false
	}
	o.turnMu.Lock()
	defer o.turnMu.Unlock()
	if _, exists := o.pendingBtw[id]; !exists {
		return false
	}
	delete(o.pendingBtw, id)
	return true
}

func (o *Orchestrator) drainBtwLocked() {
	for {
		select {
		case <-o.btwCh:
		default:
			return
		}
	}
}

func (o *Orchestrator) clearPendingBtwLocked() []string {
	if len(o.pendingBtw) == 0 {
		o.pendingBtwOrder = o.pendingBtwOrder[:0]
		return nil
	}
	cleared := make([]string, 0, len(o.pendingBtw))
	for _, id := range o.pendingBtwOrder {
		if _, exists := o.pendingBtw[id]; exists {
			cleared = append(cleared, id)
			delete(o.pendingBtw, id)
		}
	}
	o.pendingBtwOrder = o.pendingBtwOrder[:0]
	return cleared
}

func (o *Orchestrator) subAgentRunner() *SubAgentRunner {
	if o == nil || o.session == nil {
		return nil
	}
	return o.session.SubAgentRunner()
}
