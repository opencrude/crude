package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"sync"

	"crude/document"
	"github.com/fsnotify/fsnotify"
)

// One directory watcher follows atomic replacement as well as in-place writes.
// Notifications are delivered by this worker, never from a document lock/actor.
type documentWatch struct {
	s          *DocumentService
	watcher    *fsnotify.Watcher
	mu         sync.Mutex
	pending    map[string]bool
	wake       chan struct{}
	stop, done chan struct{}
	once       sync.Once
}

func newDocumentWatch(s *DocumentService) *documentWatch {
	w := &documentWatch{s: s, pending: make(map[string]bool), wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go w.run()
	return w
}
func (w *documentWatch) add(dir string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.stop:
		return context.Canceled
	default:
	}
	if w.watcher == nil {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return err
		}
		w.watcher = watcher
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
	// Watch ancestors as well: ReadDirectoryChangesW reports a directory rename
	// to its parent, not to a handle watching only the directory's contents.
	// fsnotify owns registration identity and repeated Add is idempotent.
	root := w.s.root
	if !documentUnder(root, dir) {
		root = filepath.Dir(dir)
	}
	for {
		if err := w.watcher.Add(dir); err != nil {
			return err
		}
		parent := filepath.Dir(dir)
		if parent == dir || dir == root {
			return nil
		}
		dir = parent
	}
}

// Removed/renamed directories no longer own a watch, even on platforms that
// silently remove it. Reopening a recreated path must register a fresh watch.
func (w *documentWatch) forget(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.watcher == nil {
		return
	}
	for _, dir := range w.watcher.WatchList() {
		if documentUnder(path, dir) {
			_ = w.watcher.Remove(dir)
		}
	}
}

func (w *documentWatch) changed(id string) {
	w.mu.Lock()
	w.pending[id] = true
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (w *documentWatch) close() {
	w.once.Do(func() {
		close(w.stop)
		w.mu.Lock()
		if w.watcher != nil {
			_ = w.watcher.Close()
		}
		w.mu.Unlock()
		<-w.done
	})
}
func (w *documentWatch) run() {
	defer close(w.done)
	var events <-chan fsnotify.Event
	var failures <-chan error
	for {
		select {
		case <-w.stop:
			return
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename|fsnotify.Chmod) != 0 {
				path := filepath.Clean(event.Name)
				if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					w.forget(path)
				}
				w.s.reconcilePath(path)
			}
		case _, ok := <-failures:
			if !ok {
				failures = nil
				continue
			}
			// Overflow means any retained disk preimage may have changed.
			w.s.mu.Lock()
			ids := make([]string, 0, len(w.s.docs))
			for id := range w.s.docs {
				ids = append(ids, id)
			}
			w.s.mu.Unlock()
			for _, id := range ids {
				w.s.reconcile(id)
			}
		case <-w.wake:
			w.mu.Lock()
			if w.watcher != nil {
				events = w.watcher.Events
				failures = w.watcher.Errors
			}
			pending := w.pending
			w.pending = make(map[string]bool)
			w.mu.Unlock()
			w.s.mu.Lock()
			bus := w.s.bus
			closed := w.s.closed
			w.s.mu.Unlock()
			if bus != nil && !closed {
				for id := range pending {
					bus.Send(DocumentChangedMsg{ID: id})
				}
			}
		}
	}
}

func (s *DocumentService) reconcile(id string) {
	s.mu.Lock()
	_ = s.reconcileLocked(id) // Watcher ingress preserves a visible conflict fence.
	s.mu.Unlock()
	s.changed(id)
}

// reconcileLocked is shared by watcher ingress and checked file transactions.
func (s *DocumentService) reconcileLocked(id string) error {
	d := s.docs[id]
	if d == nil || s.closed {
		return nil
	}
	original := d
	d = stageDocument(d)
	if d.relocation != nil {
		if err := s.recoverRelocationsLocked(); err != nil {
			original.snapshot.Conflict = true
			return err
		}
		return nil
	}
	text, exists, err := readLiveDocument(d.snapshot.Path)
	if err != nil {
		d.snapshot.Conflict = true
		persistErr := s.persistDocument(d)
		if persistErr == nil {
			*original = *d
		} else {
			original.snapshot.Conflict = true
		}
		return errors.Join(err, persistErr)
	}
	hash := sha256.Sum256(text)
	if !exists && d.snapshot.DiskExists && d.relocation == nil {
		// Removal is not a text edit. Preserve the retained text and history;
		// a save cannot silently recreate a deliberately removed resource.
		d.snapshot.Conflict = true
		persistErr := s.persistDocument(d)
		if persistErr == nil {
			*original = *d
		} else {
			original.snapshot.Conflict = true
		}
		return persistErr
	}
	if d.diskIntent != nil && hash == d.diskIntent.Hash && exists == d.diskIntent.Exists {
		d.snapshot.Saved = bytes.Clone(text)
		d.snapshot.DiskHash = hash
		d.snapshot.DiskExists = exists
		d.snapshot.Dirty = !bytes.Equal(d.snapshot.Text, text)
		d.snapshot.Conflict = false
		d.diskIntent = nil
	} else if hash == d.snapshot.DiskHash && exists == d.snapshot.DiskExists && d.diskIntent == nil {
		return nil
	}
	if d.diskIntent == nil && hash == d.snapshot.DiskHash && exists == d.snapshot.DiskExists {
		// Exact pending disk commit reconciled above.
	} else if d.snapshot.Dirty || d.diskIntent != nil {
		// Preserve both local work and its original disk precondition; save must
		// still fail closed rather than authorizing overwrite of external bytes.
		d.snapshot.Conflict = true
	} else {
		c := document.Change{DocumentID: id, RequestID: "disk", BaseRevision: d.snapshot.Revision, Edits: []document.Edit{{Start: 0, End: len(d.snapshot.Text), Text: string(text)}}}
		if _, err = s.commit(d, c); err != nil {
			d.snapshot.Conflict = true
		} else {
			d.snapshot.Saved = bytes.Clone(text)
			d.snapshot.DiskHash = hash
			d.snapshot.DiskExists = exists
			d.snapshot.Dirty = false
			d.snapshot.Conflict = false
		}
	}
	if persistErr := s.persistDocument(d); persistErr != nil {
		// Never install unpersisted text/history, but prevent a stale clean
		// snapshot from authorizing a save or a second history mutation.
		original.snapshot.Conflict = true
		return errors.Join(err, persistErr)
	} else {
		previousRevision := original.snapshot.Revision
		*original = *d
		if s.lsp != nil && d.lspOpen && d.snapshot.Revision != previousRevision {
			s.lsp.UpdateFile(context.Background(), d.snapshot.Path, string(d.snapshot.Text))
		}
	}
	return err
}

// ReconcileDisk is an off-actor explicit reconciliation barrier for callers
// requiring settled disk state, independent of filesystem event timing.
func (s *DocumentService) ReconcileDisk(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	err := s.recoverRelocationsLocked()
	if err == nil {
		err = s.reconcileLocked(id)
	}
	s.mu.Unlock()
	s.changed(id)
	if err != nil {
		return err
	}
	return ctx.Err()
}

func (s *DocumentService) reconcilePath(path string) {
	s.mu.Lock()
	var changed []string
	for id, d := range s.docs {
		// A directory event invalidates every retained descendant, not just a
		// document whose exact path happens to equal the event's name.
		if documentUnder(path, d.snapshot.Path) {
			_ = s.reconcileLocked(id) // Failure is retained as a conflict fence.
			changed = append(changed, id)
		}
	}
	s.mu.Unlock()
	for _, id := range changed {
		s.changed(id)
	}
}
