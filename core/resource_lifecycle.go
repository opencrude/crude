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
		}
	}()
	return l
}

// submit is actor-safe, bounded and nonblocking. Check capacity before recording
// a mutation receipt; rejected callbacks have not been admitted.
func (l *resourceLane) submit(run func(context.Context)) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.ctx.Err() != nil || run == nil {
		return false
	}
	select {
	case l.queue <- run:
		return true
	default:
		return false
	}
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
