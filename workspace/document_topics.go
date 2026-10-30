package workspace

import (
	"bytes"
	"sync"

	"crude/document"
)

// SubscribeDocument observes committed document changes directly, without an
// actor or event-bus round trip. Notifications conflate; TopicDocument reads the
// authoritative journal under the same mutex as mutation and admission.
func (s *DocumentService) SubscribeDocument(owner document.Owner, id string) (<-chan struct{}, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.owned(owner, id); err != nil {
		return nil, nil, err
	}
	if s.subscribers == nil {
		s.subscribers = make(map[string]map[chan struct{}]bool)
	}
	if s.subscribers[id] == nil {
		s.subscribers[id] = make(map[chan struct{}]bool)
	}
	wake := make(chan struct{}, 1)
	s.subscribers[id][wake] = true
	var once sync.Once
	return wake, func() { once.Do(func() { s.mu.Lock(); delete(s.subscribers[id], wake); s.mu.Unlock() }) }, nil
}

// TopicDocument returns the complete contiguous journal suffix and metadata.
// It never copies full text on a delta, and copies saved bytes only on change.
func (s *DocumentService) TopicDocument(owner document.Owner, id string, after uint64, saved []byte) (document.SyncResult, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.owned(owner, id)
	if err != nil {
		return document.SyncResult{}, false, err
	}
	out := s.syncDocumentLocked(d, after)
	if out.Reset {
		return out, true, nil
	}
	savepoint := !bytes.Equal(saved, d.snapshot.Saved)
	out.Snapshot.Text = nil
	if !savepoint {
		out.Snapshot.Saved = nil
	} else {
		out.Snapshot.Saved = bytes.Clone(out.Snapshot.Saved)
	}
	return out, savepoint, nil
}

// notifyDocument must run after releasing s.mu, including for grouped execution
// apply/undo. Wakes carry no document state: a reader always obtains the latest
// committed journal under s.mu, even when later commits conflate notifications.
// Admission, release and these nonblocking sends share the mutex; channels are
// never closed, so unsubscribe/Close cannot race a send into a closed channel.
func (s *DocumentService) notifyDocument(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.Insight.observeLocked(s.docs[id])
	for wake := range s.subscribers[id] {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}
