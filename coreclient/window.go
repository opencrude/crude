package coreclient

import (
	"context"
	"errors"
	"sync"

	"crude/coreprotocol"
)

var errWindowClosed = errors.New("core client closed while a command waited for capacity")

// commandWindow mirrors the core's retained-command capacity for this
// session. A command holds its slot from its first write until the core
// confirms it holds no result (final ACK, refusal or delivered release).
// Waiting here replaces a "busy" refusal; a caller's context bounds the wait.
// The zero value is ready to use.
type commandWindow struct {
	mu      sync.Mutex
	count   int
	bytes   int
	changed chan struct{} // closed and replaced whenever a slot is freed
}

// acquire waits for one slot of size bytes. An oversized request may run
// alone; the core reports its own bound.
func (w *commandWindow) acquire(ctx context.Context, done <-chan struct{}, size int) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-done:
			return errWindowClosed
		default:
		}
		w.mu.Lock()
		if w.count == 0 || w.count < coreprotocol.SessionRetainedCommands && w.bytes+size <= coreprotocol.SessionRetainedCommandBytes {
			w.count++
			w.bytes += size
			w.mu.Unlock()
			return nil
		}
		if w.changed == nil {
			w.changed = make(chan struct{})
		}
		changed := w.changed
		w.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return errWindowClosed
		}
	}
}

func (w *commandWindow) release(size int) {
	w.mu.Lock()
	w.count--
	w.bytes -= size
	if w.changed != nil {
		close(w.changed)
		w.changed = nil
	}
	w.mu.Unlock()
}
