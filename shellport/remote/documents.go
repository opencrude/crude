package remote

import (
	"context"
	"sync"
	"time"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/workspace"
)

type documentDeadline struct {
	timer *time.Timer
	due   workspace.DocumentAutosaveDueMsg
}

// documentService owns only client-local deadlines, never editor text or disk.
type documentService struct {
	w         *Workspace
	mu        sync.Mutex
	deadlines map[string]*documentDeadline
	epoch     uint64
	closed    bool
}

var _ shellport.DocumentService = (*documentService)(nil)

func newDocumentService(w *Workspace) *documentService {
	return &documentService{w: w, deadlines: make(map[string]*documentDeadline)}
}

func (w *Workspace) Documents() shellport.DocumentService { return w.documents }
func (w *Workspace) EnsureDocuments(_ *workspace.EventBus) shellport.DocumentService {
	return w.documents
}

func (s *documentService) Schedule(bufferID, path string, revision uint64, delay time.Duration) {
	if bufferID == "" || path == "" || delay <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if old := s.deadlines[bufferID]; old != nil {
		old.timer.Stop()
	}
	s.epoch++
	deadline := &documentDeadline{due: workspace.DocumentAutosaveDueMsg{BufferID: bufferID, Path: path, Revision: revision, Epoch: s.epoch}}
	s.deadlines[bufferID] = deadline
	deadline.timer = time.AfterFunc(delay, func() { s.publishDue(deadline) })
}

func (s *documentService) publishDue(deadline *documentDeadline) {
	s.mu.Lock()
	valid := !s.closed && s.deadlines[deadline.due.BufferID] == deadline
	s.mu.Unlock()
	if valid {
		// Cancellation after this check is fenced again by AcceptDue on Update.
		s.w.send(deadline.due)
	}
}

func (s *documentService) AcceptDue(msg workspace.DocumentAutosaveDueMsg) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	deadline := s.deadlines[msg.BufferID]
	if s.closed || deadline == nil || deadline.due != msg {
		return false
	}
	deadline.timer.Stop()
	delete(s.deadlines, msg.BufferID)
	return true
}

func (s *documentService) Cancel(bufferID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if deadline := s.deadlines[bufferID]; deadline != nil {
		deadline.timer.Stop()
		delete(s.deadlines, bufferID)
	}
}

func (s *documentService) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for id, deadline := range s.deadlines {
		deadline.timer.Stop()
		delete(s.deadlines, id)
	}
}

// Save is command-owned; Schedule, Cancel and AcceptDue never issue RPCs.
func (s *documentService) Save(ctx context.Context, request workspace.DocumentSaveRequest) workspace.DocumentSaveResult {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return workspace.DocumentSaveResult{Request: request, Err: context.Canceled}
	}
	var result coreprotocol.DocumentSnapshotSaveResult
	if err := s.w.call(ctx, coreprotocol.DocumentSnapshotSave, &coreprotocol.DocumentSnapshotSaveRequest{Request: request}, &result); err != nil {
		return workspace.DocumentSaveResult{Request: request, Err: err}
	}
	return result.DocumentResult()
}
