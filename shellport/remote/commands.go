package remote

import (
	"context"
	"sync"
)

// commandQueue preserves the order of a shell's fire-and-forget mutations.
// push never blocks the Update goroutine; one worker sends in FIFO order, so
// "stop" can never overtake the "steer" typed before it.
type commandQueue struct {
	mu    sync.Mutex
	items []func(context.Context)
	ready chan struct{}
}

func (q *commandQueue) init() { q.ready = make(chan struct{}, 1) }

func (q *commandQueue) push(fn func(context.Context)) {
	q.mu.Lock()
	q.items = append(q.items, fn)
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

func (q *commandQueue) pop() (func(context.Context), bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil, false
	}
	fn := q.items[0]
	q.items[0] = nil
	q.items = q.items[1:]
	return fn, true
}

func (w *Workspace) runCommands() {
	defer w.wg.Done()
	for {
		select {
		case <-w.stop:
			return
		case <-w.commands.ready:
		}
		for {
			fn, ok := w.commands.pop()
			if !ok {
				break
			}
			ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
			fn(ctx)
			cancel()
			select {
			case <-w.stop:
				return
			default:
			}
		}
	}
}
