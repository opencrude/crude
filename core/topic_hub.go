package core

import "sync"

const topicJournalEntries = 256
const topicJournalBytes = 8 << 20

type topicEntry struct {
	seq  uint64
	body []byte
}

// topicHub retains encoded immutable publications once for all subscribers.
// Slow readers hold only their current entry and cursor; they never block a
// producer or another reader. An evicted log gap requires an explicit reset.
type topicHub struct {
	mu      sync.Mutex
	seq     uint64
	bytes   int
	entries []topicEntry
	wake    chan struct{}
}

func (h *topicHub) publish(body []byte) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	if h.wake != nil {
		close(h.wake)
	}
	h.wake = make(chan struct{})
	h.entries = append(h.entries, topicEntry{h.seq, body})
	h.bytes += len(body)
	for len(h.entries) > 1 && (len(h.entries) > topicJournalEntries || h.bytes > topicJournalBytes) {
		h.bytes -= len(h.entries[0].body)
		h.entries[0] = topicEntry{}
		h.entries = h.entries[1:]
	}
	return h.seq
}

func (h *topicHub) next(since uint64) (topicEntry, <-chan struct{}, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wake == nil {
		h.wake = make(chan struct{})
	}
	if since > h.seq || len(h.entries) > 0 && since+1 < h.entries[0].seq {
		return topicEntry{}, h.wake, false
	}
	for _, e := range h.entries {
		if e.seq > since {
			return e, h.wake, true
		}
	}
	return topicEntry{}, h.wake, true
}

func (h *topicHub) sequence() uint64 { h.mu.Lock(); defer h.mu.Unlock(); return h.seq }
