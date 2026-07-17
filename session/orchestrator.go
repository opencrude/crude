package session

import (
	"sync/atomic"

	"crude/provider"
)

type Orchestrator struct {
	ID             string
	session        *Session
	InputCh        chan provider.UserInput
	BtwCh          chan provider.UserInput
	CancelCh       chan struct{}
	BlockResolveCh chan string
	subAgentSup    *SubAgentRunner
	closing        atomic.Bool
	stopCh         chan struct{}
}

func NewOrchestrator(id string, s *Session) *Orchestrator {
	return &Orchestrator{
		ID:             id,
		session:        s,
		InputCh:        make(chan provider.UserInput, 8),
		BtwCh:          make(chan provider.UserInput, 8),
		CancelCh:       make(chan struct{}, 1),
		BlockResolveCh: make(chan string, 1),
		stopCh:         make(chan struct{}),
	}
}
