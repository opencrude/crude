package core

import (
	"context"
	"sync"
)

// resourceLane serializes captured resource effects in admission order. The
// actor remains the only owner of its maps and applies results on its own loop;
// lane callbacks must not inspect or mutate actor state.
type resourceLane struct {
	mu     sync.Mutex
	queue  chan func(context.Context)
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	closed bool
	fenced bool
	active int
}

type resourceLaneObservation struct {
	Queued   int  `json:"queued"`
	Capacity int  `json:"queue_capacity"`
	Active   int  `json:"active_including_queued"`
	Closed   bool `json:"closed"`
	Fenced   bool `json:"fenced"`
}

func (l *resourceLane) observation() *resourceLaneObservation {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return &resourceLaneObservation{len(l.queue), cap(l.queue), l.active, l.closed, l.fenced}
}

func newResourceLane(ctx context.Context, capacity int) *resourceLane {
	if capacity < 1 {
		panic("resource lane capacity must be positive")
	}
	ctx, cancel := context.WithCancel(ctx)
	l := &resourceLane{queue: make(chan func(context.Context), capacity), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(l.done)
		// Drain admitted callbacks even on cancellation: each callback owns a
		// completion obligation. Context-aware effects must reject cancelled
		// work before mutation, while cleanup must still release exact owners.
		for run := range l.queue {
			run(l.ctx)
			l.mu.Lock()
			l.active--
			l.mu.Unlock()
		}
	}()
	return l
}

// submit is actor-safe, bounded and nonblocking. Check capacity before recording
// a mutation receipt; rejected callbacks have not been admitted.
func (l *resourceLane) submit(run func(context.Context)) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.fenced || l.ctx.Err() != nil || run == nil {
		return false
	}
	select {
	case l.queue <- run:
		l.active++
		return true
	default:
		return false
	}
}

func (l *resourceLane) beginIdleLease() (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.fenced || l.active != 0 {
		return nil, false
	}
	l.fenced = true
	return func() { l.mu.Lock(); l.fenced = false; l.mu.Unlock() }, true
}

// close cancels effects and joins every admitted completion. The actor must
// continue consuming callback results until this returns; do not join from its
// loop while callbacks are blocked publishing to that loop.
func (l *resourceLane) close() {
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		l.cancel()
		close(l.queue)
	}
	l.mu.Unlock()
	<-l.done
}
