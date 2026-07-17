package workspace

import (
	"context"
	"sync"
	"time"

	"crude/fileio"
)

// ProjectSearchRequest is an immutable search operation created by
// ProjectSearchService.Begin. Execute may run on a Bubble Tea command goroutine.
type ProjectSearchRequest struct {
	ID      uint64
	Root    string
	Query   string
	Options fileio.ProjectSearchOptions
	ctx     context.Context
}

// ProjectSearchResult is domain output for one request.
type ProjectSearchResult struct {
	RequestID uint64
	Matches   []fileio.ProjectSearchMatch
	Err       error
	Duration  time.Duration
}

// ProjectSearchCompletedMsg is published through the workspace event bus after
// the service accepts a current (non-stale) result.
type ProjectSearchCompletedMsg struct {
	Result ProjectSearchResult
}

// ProjectSearchService owns recursive search execution identity and cancellation.
// UI widgets emit search intents and display results; they never create contexts or
// call fileio.SearchProject directly.
type ProjectSearchService struct {
	mu        sync.Mutex
	nextID    uint64
	currentID uint64
	cancel    context.CancelFunc
}

// NewProjectSearchService creates an idle workspace search service.
func NewProjectSearchService() *ProjectSearchService {
	return &ProjectSearchService{}
}

// Begin cancels the previous operation and returns a new immutable request.
func (s *ProjectSearchService) Begin(root, query string, opts fileio.ProjectSearchOptions) ProjectSearchRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.nextID++
	ctx, cancel := context.WithCancel(context.Background())
	s.currentID = s.nextID
	s.cancel = cancel
	return ProjectSearchRequest{
		ID:      s.currentID,
		Root:    root,
		Query:   query,
		Options: opts,
		ctx:     ctx,
	}
}

// Execute performs one request synchronously. Callers choose the goroutine or
// command context; the service owns the cancellation context and request identity.
func (s *ProjectSearchService) Execute(req ProjectSearchRequest) ProjectSearchResult {
	started := time.Now()
	ctx := req.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	matches, err := fileio.SearchProject(ctx, req.Root, req.Query, req.Options)
	return ProjectSearchResult{
		RequestID: req.ID,
		Matches:   matches,
		Err:       err,
		Duration:  time.Since(started),
	}
}

// Accept completes result if it still belongs to the active request. Stale and
// canceled results are rejected without exposing service state to the widget.
func (s *ProjectSearchService) Accept(result ProjectSearchResult) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if result.RequestID == 0 || result.RequestID != s.currentID {
		return false
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel = nil
	return true
}

// Cancel stops the current search and invalidates all outstanding results.
func (s *ProjectSearchService) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel = nil
	s.nextID++
	s.currentID = s.nextID
}

// StartProjectSearch starts a workspace-rooted search. Completion is published
// through EventBus as ProjectSearchCompletedMsg; no domain result is returned to
// the TUI method caller.
func (w *Workspace) StartProjectSearch(query string, opts fileio.ProjectSearchOptions) uint64 {
	if w.ProjectSearch == nil {
		w.ProjectSearch = NewProjectSearchService()
	}
	request := w.ProjectSearch.Begin(w.RootPath, query, opts)
	service := w.ProjectSearch
	bus := w.eventBus
	go func() {
		result := service.Execute(request)
		if !service.Accept(result) {
			return
		}
		if bus != nil {
			bus.Send(ProjectSearchCompletedMsg{Result: result})
		}
	}()
	return request.ID
}

// CancelProjectSearch stops the active workspace project search.
func (w *Workspace) CancelProjectSearch() {
	if w.ProjectSearch != nil {
		w.ProjectSearch.Cancel()
	}
}
