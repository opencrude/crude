package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"crude/fileio"
)

var ErrDocumentConflict = errors.New("document changed on disk")

// DocumentSaveRequest is an immutable buffer snapshot submitted to the one
// workspace disk authority.
type DocumentSaveRequest struct {
	BufferID      string
	Path          string
	Data          []byte
	Revision      uint64
	ExpectedHash  [sha256.Size]byte
	ExpectedExist bool
	Reason        DocumentSaveReason
}

type DocumentSaveReason string

const (
	DocumentSaveManual     DocumentSaveReason = "manual"
	DocumentSaveAfterDelay DocumentSaveReason = "after_delay"
	DocumentSaveFocusOut   DocumentSaveReason = "focus_out"
)

type DocumentSaveResult struct {
	Request   DocumentSaveRequest
	DiskHash  [sha256.Size]byte
	Unchanged bool
	Committed bool
	Err       error
}

// DocumentAutosaveDueMsg asks the frontend to capture the still-current live
// buffer. The scheduler never owns mutable editor text.
type DocumentAutosaveDueMsg struct {
	BufferID string
	Path     string
	Revision uint64
	Epoch    uint64
}

type documentDeadline struct {
	timer    *time.Timer
	epoch    uint64
	path     string
	revision uint64
}

// DocumentSaveService is the sole owner of editor-save deadlines and disk
// commits for a workspace. It never owns live text.
type DocumentSaveService struct {
	mu        sync.Mutex
	deadlines map[string]*documentDeadline
	epoch     uint64
	bus       *EventBus
	closed    bool
	active    map[*DocumentSaveRequest]context.CancelFunc
	writes    sync.WaitGroup
	slots     chan struct{}
}

func NewDocumentSaveService() *DocumentSaveService {
	return &DocumentSaveService{
		deadlines: make(map[string]*documentDeadline),
		active:    make(map[*DocumentSaveRequest]context.CancelFunc),
		slots:     make(chan struct{}, 4),
	}
}

func (s *DocumentSaveService) SetEventBus(bus *EventBus) {
	s.mu.Lock()
	s.bus = bus
	s.mu.Unlock()
}

func (s *DocumentSaveService) Schedule(bufferID, path string, revision uint64, delay time.Duration) {
	if s == nil || bufferID == "" || path == "" || delay <= 0 {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.epoch++
	epoch := s.epoch
	if current := s.deadlines[bufferID]; current != nil && current.timer.Stop() {
		current.epoch, current.path, current.revision = epoch, path, revision
		current.timer.Reset(delay)
		s.mu.Unlock()
		return
	}
	deadline := &documentDeadline{epoch: epoch, path: path, revision: revision}
	deadline.timer = time.AfterFunc(delay, func() { s.publishDue(bufferID, deadline) })
	s.deadlines[bufferID] = deadline
	s.mu.Unlock()
}

func (s *DocumentSaveService) publishDue(bufferID string, deadline *documentDeadline) {
	s.mu.Lock()
	current, ok := s.deadlines[bufferID]
	if !ok || current != deadline || s.closed {
		s.mu.Unlock()
		return
	}
	bus := s.bus
	s.mu.Unlock()
	if bus != nil {
		bus.Send(DocumentAutosaveDueMsg{BufferID: bufferID, Path: deadline.path, Revision: deadline.revision, Epoch: deadline.epoch})
	}
}

func (s *DocumentSaveService) AcceptDue(msg DocumentAutosaveDueMsg) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.deadlines[msg.BufferID]
	if s.closed || !ok || current.epoch != msg.Epoch {
		return false
	}
	delete(s.deadlines, msg.BufferID)
	return true
}

func (s *DocumentSaveService) Cancel(bufferID string) {
	if s == nil || bufferID == "" {
		return
	}
	s.mu.Lock()
	if current, ok := s.deadlines[bufferID]; ok {
		current.timer.Stop()
		delete(s.deadlines, bufferID)
	}
	s.mu.Unlock()
}

// PendingDeadlines reports how many buffers currently hold an armed automatic
// save deadline. Owners use it to verify that cancel and re-arm sequences leave
// at most one timer per buffer.
func (s *DocumentSaveService) PendingDeadlines() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deadlines)
}

func (s *DocumentSaveService) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	for id, deadline := range s.deadlines {
		deadline.timer.Stop()
		delete(s.deadlines, id)
	}
	for _, cancel := range s.active {
		cancel()
	}
	s.mu.Unlock()
	s.writes.Wait()
}

func (s *DocumentSaveService) Save(ctx context.Context, request DocumentSaveRequest) DocumentSaveResult {
	result := DocumentSaveResult{Request: request}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		result.Err = context.Canceled
		return result
	}
	ctx, cancel := context.WithCancel(ctx)
	s.active[&request] = cancel
	s.writes.Add(1)
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.active, &request)
		s.mu.Unlock()
		s.writes.Done()
	}()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		result.Err = ctx.Err()
		return result
	}
	if request.Path == "" {
		result.Err = fmt.Errorf("save document: empty path")
		return result
	}
	path, err := filepath.Abs(request.Path)
	if err != nil {
		result.Err = fmt.Errorf("save document: resolve path: %w", err)
		return result
	}
	request.Path = path
	result.Request.Path = path
	parent := filepath.Dir(path)
	if err := fileio.CheckDirectoryNoSymlinks(parent); err != nil {
		result.Err = fmt.Errorf("save document: parent must be an existing non-symlink directory")
		return result
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		result.Err = err
		return result
	}
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		result.Err = err
		return result
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		result.Err = err
		return result
	}

	current, mode, exists, err := readDocument(path)
	if err != nil {
		result.Err = err
		return result
	}
	if exists != request.ExpectedExist || exists && sha256.Sum256(current) != request.ExpectedHash {
		result.Err = ErrDocumentConflict
		return result
	}
	if !exists && request.Reason != DocumentSaveManual {
		result.Err = fmt.Errorf("automatic save requires an existing file")
		return result
	}
	if exists && bytes.Equal(current, request.Data) {
		result.DiskHash = sha256.Sum256(current)
		result.Unchanged = true
		return result
	}
	if err := fileio.WriteAtomicChecked(path, request.Data, mode, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fileio.CheckDirectoryNoSymlinks(parent); err != nil {
			return ErrDocumentConflict
		}
		parentNow, err := os.Stat(parent)
		if err != nil || !os.SameFile(parentInfo, parentNow) {
			return ErrDocumentConflict
		}
		latest, latestMode, latestExists, err := readDocument(path)
		if err != nil {
			return err
		}
		if latestExists != request.ExpectedExist || latestExists && (sha256.Sum256(latest) != request.ExpectedHash || latestMode != mode) {
			return ErrDocumentConflict
		}
		return nil
	}); err != nil {
		if fileio.WriteCommitted(err) {
			result.DiskHash = sha256.Sum256(request.Data)
			result.Committed = true
		}
		result.Err = err
		return result
	}
	newHash := sha256.Sum256(request.Data)
	committed, committedMode, exists, err := readDocument(path)
	if err != nil || !exists || sha256.Sum256(committed) != newHash || committedMode != mode {
		if err != nil {
			result.Err = err
		} else {
			result.Err = ErrDocumentConflict
		}
		return result
	}
	result.DiskHash = newHash
	result.Committed = true
	return result
}

func readDocument(path string) ([]byte, os.FileMode, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fileio.MutationFileMode(0o644), false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("save document: stat: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, true, fmt.Errorf("save document: %s is not a regular file", path)
	}
	if info.Mode().Perm()&0o222 == 0 {
		return nil, 0, true, fmt.Errorf("save document: %w", os.ErrPermission)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, true, fmt.Errorf("save document: read: %w", err)
	}
	return data, info.Mode().Perm(), true, nil
}
