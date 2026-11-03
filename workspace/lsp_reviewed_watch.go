package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
)

// Reviewed edits are a single mutation across multiple files. Target events and
// later events are delivered in order after the transaction. Other changes
// invalidate semantic state immediately and revoke the transaction's fence.
type reviewedWatchRegistry struct {
	mu      sync.Mutex
	current *reviewedWatchTxn
	gap     bool
	closed  bool
}

type reviewedWatchTxn struct {
	registry *reviewedWatchRegistry
	targets  map[string]*reviewedWatchTarget
	events   []reviewedWatchEvent
	tainted  bool
	draining bool
	space    chan struct{}
}

type reviewedWatchTarget struct {
	committing, committed, removed, observed bool
}

type reviewedWatchEvent struct {
	ctx         context.Context
	path        string
	kind        int
	ready       chan struct{}
	invalidated bool
}

const maxReviewedWatchEvents = 1024

func (r *reviewedWatchRegistry) begin(edits []FileEdit) (*reviewedWatchTxn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.gap {
		return nil, errors.New("project file observation is incomplete; review the edit again after observation returns")
	}
	if r.current != nil {
		return nil, errors.New("another reviewed edit is still finishing")
	}
	tx := &reviewedWatchTxn{registry: r, targets: make(map[string]*reviewedWatchTarget, len(edits)), space: make(chan struct{})}
	for _, edit := range edits {
		path := filepath.Clean(edit.Path)
		if tx.targets[path] != nil {
			return nil, errors.New("duplicate reviewed edit target")
		}
		tx.targets[path] = &reviewedWatchTarget{}
	}
	r.current = tx
	return tx, nil
}

func (r *reviewedWatchRegistry) deferEvent(ctx context.Context, path string, kind int, invalidate func()) bool {
	path = filepath.Clean(path)
	alreadyInvalidated := false
	for {
		r.mu.Lock()
		tx := r.current
		if tx == nil || ctx.Err() != nil {
			r.mu.Unlock()
			return false
		}
		target := tx.targets[path]
		if target == nil && !tx.draining && len(tx.events) == 0 {
			// Delivery need not wait, but even the first unrelated change
			// invalidates the transaction before a final success check.
			tx.tainted = true
			r.mu.Unlock()
			return false
		}
		if len(tx.events) >= maxReviewedWatchEvents {
			if !tx.draining {
				tx.tainted = true
			}
			space := tx.space
			r.mu.Unlock()
			if !alreadyInvalidated && invalidate != nil {
				invalidate()
				alreadyInvalidated = true
			}
			select {
			case <-ctx.Done():
				return false
			case <-space:
				continue
			}
		}
		ambiguous := tx.draining || target == nil
		// An atomic replacement produces create, or remove then create on
		// Windows. Admit that sequence only for a target whose exact writer
		// commit has started. The writer still checks staged file identity and
		// the complete postimage before claiming success; a pathname event
		// alone is never proof that an external mutation belongs to us.
		if !tx.draining {
			if target == nil || target.observed || (!target.committing && !target.committed) ||
				(kind != 1 && (kind != 3 || target.removed)) {
				tx.tainted = true
				ambiguous = true
			} else if kind == 3 {
				target.removed = true
			} else {
				target.observed = true
			}
		}
		event := reviewedWatchEvent{ctx: ctx, path: path, kind: kind, invalidated: alreadyInvalidated}
		if ambiguous && invalidate != nil && !alreadyInvalidated {
			event.ready, event.invalidated = make(chan struct{}), true
		}
		tx.events = append(tx.events, event)
		r.mu.Unlock()
		if event.ready != nil {
			invalidate()
			close(event.ready)
		}
		return true
	}
}

func (r *reviewedWatchRegistry) setReliable(reliable bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gap = !reliable
	if !reliable && r.current != nil {
		r.current.tainted = true
	}
}

func (tx *reviewedWatchTxn) beginCommit(path string) error {
	r := tx.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	target := tx.targets[filepath.Clean(path)]
	if r.current != tx || tx.tainted || target == nil || target.committing || target.committed {
		return errors.New("project files changed during the reviewed edit")
	}
	target.committing = true
	return nil
}

func (tx *reviewedWatchTxn) check() error {
	r := tx.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != tx || tx.tainted {
		return errors.New("project files changed during the reviewed edit")
	}
	return nil
}

func (tx *reviewedWatchTxn) endCommit(path string, committed bool) {
	r := tx.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current == tx {
		target := tx.targets[filepath.Clean(path)]
		target.committing, target.committed = false, committed
	}
}

func (r *reviewedWatchRegistry) finish(tx *reviewedWatchTxn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != tx {
		return false
	}
	tx.draining = true
	if len(tx.events) == 0 {
		r.current = nil
		close(tx.space)
		return false
	}
	return true
}

func (r *reviewedWatchRegistry) nextReplay() (reviewedWatchEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tx := r.current
	if tx == nil || !tx.draining {
		return reviewedWatchEvent{}, false
	}
	if len(tx.events) == 0 {
		r.current = nil
		close(tx.space)
		return reviewedWatchEvent{}, false
	}
	event := tx.events[0]
	tx.events[0] = reviewedWatchEvent{}
	tx.events = tx.events[1:]
	close(tx.space)
	tx.space = make(chan struct{})
	return event, true
}

func (r *reviewedWatchRegistry) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.current != nil {
		r.current.tainted = true
		close(r.current.space)
		r.current = nil
	}
}

// Replay remains on the manager's joined worker. The registry continues to
// queue later events for these targets until all earlier ones have been sent.
func (m *LSPManager) replayReviewedWatch(tx *reviewedWatchTxn) {
	if !m.reviewWatch.finish(tx) {
		return
	}
	m.mu.Lock()
	if !m.closed {
		m.signalLocked()
	}
	m.mu.Unlock()
}
