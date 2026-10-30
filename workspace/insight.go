package workspace

import (
	"context"
	"crypto/rand"
	"errors"
	"slices"
	"sync"

	"crude/document"
	"crude/insight/facts"
	"crude/syntax"
	"crude/types"
)

// All workspaces in a core process share this native execution admission.
// Each workspace owns its queue, one worker and at most one reusable session.
// Native memory is not included in Go allocation counters.
var insightParseSlots = make(chan struct{}, 2)

const insightSpanLimit = 64 << 10 // bounded wire projection, not semantic coverage
const insightResultBudget = 16 << 20

type insightEntry struct {
	view        types.DocumentInsight
	queued      bool
	cancel      context.CancelFunc
	subscribers map[chan struct{}]bool
}

// InsightService owns derived intelligence for canonical documents. Document
// mutation only offers identities; the worker reads immutable text off-thread.
// No parser, query, I/O or full-text copy runs in the mutation path.
type InsightService struct {
	documents                *DocumentService
	mu                       sync.Mutex
	entries                  map[string]*insightEntry
	queue                    []*insightEntry
	version                  uint64
	generation               string
	bytes                    int
	ctx                      context.Context
	cancel                   context.CancelFunc
	wake                     chan struct{}
	done                     chan struct{}
	diagnostics              *facts.Store
	diagnosticDetail         string
	diagnosticSummaryVersion uint64
	diagnosticSummaryEnabled bool
	diagnosticSummaryFloor   string
	diagnosticSummaryCached  bool
	diagnosticSummary        types.ProjectInsights
}

func newInsightService(documents *DocumentService) *InsightService {
	ctx, cancel := context.WithCancel(context.Background())
	s := &InsightService{documents: documents, entries: make(map[string]*insightEntry), generation: rand.Text(),
		ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	s.diagnostics, _ = facts.New(4096, 32768)
	go s.run()
	return s
}

func (s *InsightService) Close() {
	if s == nil {
		return
	}
	s.cancel()
	<-s.done
}

func (s *InsightService) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *InsightService) publishLocked(entry *insightEntry) {
	s.version++
	entry.view.Version = s.version
	for wake := range entry.subscribers {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func insightWeight(view types.DocumentInsight) int {
	if view.Syntax == nil {
		return 0
	}
	return len(view.Syntax.Spans)*12 + len(view.Syntax.Problems)*12
}

// observeLocked runs under the document authority lock. Source bytes are not
// retained here. Save-only/selection/focus notifications do not invalidate it.
func (s *InsightService) observeLocked(d *liveDocument) {
	if s == nil || d == nil {
		return
	}
	id, path, revision := d.snapshot.ID, d.snapshot.Path, d.snapshot.Revision
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	entry := s.entries[id]
	if len(d.owners) == 0 {
		if entry != nil {
			if entry.cancel != nil {
				entry.cancel()
			}
			s.bytes -= insightWeight(entry.view)
			entry.view.Syntax = nil
			// A raw subscriber may outlive its owner's view release. Wake it so
			// the next authoritative read refuses rather than retaining a stream.
			s.publishLocked(entry)
			delete(s.entries, id)
			s.queue = slices.DeleteFunc(s.queue, func(queued *insightEntry) bool { return queued == entry })
			s.signal()
		}
		return
	}
	if entry != nil && entry.view.Revision == revision && entry.view.Path == path {
		return
	}
	if entry == nil {
		entry = &insightEntry{subscribers: make(map[chan struct{}]bool)}
		s.entries[id] = entry
	}
	if entry.cancel != nil {
		entry.cancel()
	}
	s.bytes -= insightWeight(entry.view)
	entry.view = types.DocumentInsight{DocumentID: id, Path: path, Revision: revision, Generation: s.generation, State: types.InsightPending}
	if _, supported := syntax.LanguageForPath(path); !supported {
		entry.view.State = types.InsightUnsupported
		entry.view.Detail = "No bundled native grammar; lexical highlighting is separate from project intelligence."
	} else if !entry.queued {
		entry.queued = true
		s.queue = append(s.queue, entry)
	}
	s.publishLocked(entry)
	s.signal()
}

func (s *InsightService) Subscribe(owner document.Owner, id string) (<-chan struct{}, func(), error) {
	// Ownership and lifecycle must be checked even for an unsupported language.
	s.documents.mu.Lock()
	defer s.documents.mu.Unlock()
	d, err := s.documents.owned(owner, id)
	if err != nil {
		return nil, nil, err
	}
	s.observeLocked(d)
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[id]
	if entry == nil {
		return nil, nil, context.Canceled
	}
	wake := make(chan struct{}, 1)
	entry.subscribers[wake] = true
	var once sync.Once
	return wake, func() {
		once.Do(func() { s.mu.Lock(); delete(entry.subscribers, wake); s.mu.Unlock() })
	}, nil
}

func cloneInsight(view types.DocumentInsight) types.DocumentInsight {
	if view.Syntax != nil {
		result := *view.Syntax
		result.Spans = slices.Clone(result.Spans)
		result.Problems = slices.Clone(result.Problems)
		view.Syntax = &result
	}
	return view
}

// Snapshot is an explicit bounded read, not a post-edit polling contract.
func (s *InsightService) Snapshot(owner document.Owner, id string) (types.DocumentInsight, error) {
	s.documents.mu.Lock()
	defer s.documents.mu.Unlock()
	if _, err := s.documents.owned(owner, id); err != nil {
		return types.DocumentInsight{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[id]
	if entry == nil || s.ctx.Err() != nil {
		return types.DocumentInsight{}, context.Canceled
	}
	return cloneInsight(entry.view), nil
}

func (s *InsightService) source(view types.DocumentInsight) ([]byte, bool) {
	s.documents.mu.Lock()
	defer s.documents.mu.Unlock()
	d := s.documents.docs[view.DocumentID]
	if s.documents.closed || d == nil || len(d.owners) == 0 ||
		d.snapshot.Revision != view.Revision || d.snapshot.Path != view.Path {
		return nil, false
	}
	// Canonical text is immutable after commit. Parse makes its own retained
	// copy under execution admission, not under the document mutex.
	return d.snapshot.Text, true
}

func (s *InsightService) run() {
	defer close(s.done)
	var session *syntax.Session
	var cached *insightEntry
	var cachedPath string
	defer func() { session.Close() }()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		}
		for {
			s.mu.Lock()
			dropped := cached != nil && (s.entries[cached.view.DocumentID] != cached || cached.view.Path != cachedPath)
			if dropped {
				s.mu.Unlock()
				session.Close()
				session, cached = nil, nil
				s.mu.Lock()
			}
			if len(s.queue) == 0 {
				s.mu.Unlock()
				break
			}
			entry := s.queue[0]
			s.queue[0] = nil
			s.queue = s.queue[1:]
			id := entry.view.DocumentID
			if s.entries[id] != entry || !entry.queued {
				s.mu.Unlock()
				continue
			}
			entry.queued = false
			if entry.view.State != types.InsightPending {
				s.mu.Unlock()
				continue
			}
			view := entry.view
			ctx, cancel := context.WithCancel(s.ctx)
			entry.cancel = cancel
			s.mu.Unlock()

			select {
			case insightParseSlots <- struct{}{}:
			case <-ctx.Done():
				cancel()
				continue
			}
			var result syntax.Result
			var err error
			source, current := s.source(view)
			if current && ctx.Err() == nil {
				if cached != entry || cachedPath != view.Path {
					session.Close()
					session, cached = nil, nil
				}
				if session == nil {
					language, _ := syntax.LanguageForPath(view.Path)
					session, err = syntax.New(language)
					if err == nil {
						cached = entry
						cachedPath = view.Path
					}
				}
				if err == nil {
					result, err = session.Parse(ctx, view.Revision, source)
				}
			}
			<-insightParseSlots

			s.documents.mu.Lock()
			canonical := s.documents.docs[id]
			current = current && !s.documents.closed && canonical != nil && len(canonical.owners) != 0 &&
				canonical.snapshot.Revision == view.Revision && canonical.snapshot.Path == view.Path
			s.mu.Lock()
			// The entry token, source revision, path and cancellation fence late
			// completion after edits, rename, close/reopen or workspace teardown.
			if current && ctx.Err() == nil && s.entries[id] == entry &&
				entry.view.Revision == view.Revision && entry.view.Path == view.Path {
				entry.view.State = types.InsightCurrent
				switch {
				case err != nil:
					entry.view.State, entry.view.Detail = types.InsightFailed, err.Error()
					if errors.Is(err, syntax.ErrLimit) {
						entry.view.State = types.InsightLimited
					}
				case len(result.Spans) > insightSpanLimit ||
					s.bytes+len(result.Spans)*12+len(result.Problems)*12 > insightResultBudget:
					entry.view.State = types.InsightLimited
					entry.view.Detail = "Native syntax projection exceeds the workspace result budget."
				default:
					entry.view.Syntax = &result
					s.bytes += insightWeight(entry.view)
				}
				s.publishLocked(entry)
			}
			s.mu.Unlock()
			s.documents.mu.Unlock()
			cancel()
		}
	}
}
