package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"crude/fileio"
	"crude/localhistory"
	"crude/session"
	ttypes "crude/types"
)

type HistorySnapshotCreated struct{ Snapshot localhistory.SnapshotRecord }
type HistoryServiceStatus struct {
	Ready     bool
	Capturing bool
	Restoring bool
	Error     string
}

type historyScopeState struct {
	sessionID  string
	messageID  string
	beforeID   string
	collisions map[string]bool
}

type LocalHistoryService struct {
	root   string
	store  *localhistory.Store
	logger Logger

	mu         sync.Mutex
	activityMu sync.RWMutex
	settings   ttypes.LocalHistorySettings
	bus        *EventBus
	cancel     context.CancelFunc
	done       chan struct{}
	generation uint64
	ready      bool
	capturing  bool
	lastErr    error
	pending    map[string]bool
	active     map[string]*historyScopeState
	suppress   bool
	restoring  bool
	stopping   bool
	opWg       sync.WaitGroup
}

func NewLocalHistoryService(root string, store *localhistory.Store, logger Logger) *LocalHistoryService {
	return &LocalHistoryService{root: root, store: store, logger: logger, pending: make(map[string]bool), active: make(map[string]*historyScopeState)}
}

func (s *LocalHistoryService) SetEventBus(bus *EventBus) { s.mu.Lock(); s.bus = bus; s.mu.Unlock() }

func (s *LocalHistoryService) UpdateSettings(settings ttypes.LocalHistorySettings) {
	effective := ttypes.AppSettings{LocalHistory: settings}
	ttypes.ApplyAppDefaults(&effective)
	settings = effective.LocalHistory
	s.mu.Lock()
	s.settings = settings
	s.generation++
	generation := s.generation
	if s.cancel != nil {
		s.cancel()
	}
	oldDone := s.done
	s.cancel = nil
	s.done = nil
	s.ready = false
	s.lastErr = nil
	if !settings.EnabledValue() || s.store == nil {
		s.done = oldDone
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	s.mu.Unlock()
	go func() {
		if oldDone != nil {
			<-oldDone
		}
		s.run(ctx, done, generation, settings)
	}()
}

func (s *LocalHistoryService) Stop() {
	s.mu.Lock()
	s.stopping = true
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	s.opWg.Wait()
}

func (s *LocalHistoryService) Status() HistoryServiceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := HistoryServiceStatus{Ready: s.ready, Capturing: s.capturing, Restoring: s.restoring}
	if s.lastErr != nil {
		status.Error = s.lastErr.Error()
	}
	return status
}

func historyOptions(settings ttypes.LocalHistorySettings, kind, label, parent string) localhistory.SnapshotOptions {
	return localhistory.SnapshotOptions{
		Kind: kind, Label: label, ParentID: parent,
		DisableGitIgnore:    !settings.UseGitIgnoreValue(),
		ExtraIgnorePatterns: append([]string(nil), settings.ExtraIgnorePatterns...),
		MaxFileSize:         int64(settings.MaxFileSizeMB) * 1024 * 1024,
	}
}

func (s *LocalHistoryService) run(ctx context.Context, done chan struct{}, generation uint64, settings ttypes.LocalHistorySettings) {
	defer close(done)
	records, timelineErr := s.store.Timeline(1)
	if timelineErr != nil {
		s.setError(generation, timelineErr)
		return
	}
	kind, label := "baseline", "Workspace baseline"
	if len(records) > 0 {
		kind, label = "startup_reconcile", "Startup reconciliation"
	}
	baseline, _, err := s.captureIfChanged(ctx, historyOptions(settings, kind, label, ""))
	if err != nil {
		s.setError(generation, err)
		return
	}
	if err := s.store.RecoverInterruptedScopes(baseline.ID, time.Now()); err != nil {
		s.setError(generation, err)
		return
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		s.setError(generation, err)
		return
	}
	defer watcher.Close()
	ignore, err := s.store.IgnoreFilter(historyOptions(settings, "", "", ""))
	if err != nil {
		s.setError(generation, err)
		return
	}
	if err := addHistoryWatchDirs(s.root, s.root, watcher, ignore); err != nil {
		s.setError(generation, err)
		return
	}
	s.mu.Lock()
	if generation == s.generation {
		s.ready = true
	}
	bus := s.bus
	s.mu.Unlock()
	if bus != nil {
		bus.Send(HistoryServiceStatus{Ready: true})
	}
	debounce := time.Duration(settings.DebounceMS) * time.Millisecond
	if debounce < 100*time.Millisecond {
		debounce = 100 * time.Millisecond
	}
	reconcile := time.Duration(settings.ReconcileIntervalSeconds) * time.Second
	if reconcile < 5*time.Second {
		reconcile = 5 * time.Second
	}
	ticker := time.NewTicker(reconcile)
	defer ticker.Stop()
	var timer *time.Timer
	var timerC <-chan time.Time
	resetTimer := func() {
		if timer == nil {
			timer = time.NewTimer(debounce)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(debounce)
		}
		timerC = timer.C
	}
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			rel, relErr := filepath.Rel(s.root, event.Name)
			if relErr != nil {
				continue
			}
			info, statErr := os.Lstat(event.Name)
			isDir := statErr == nil && info.IsDir()
			if ignore(filepath.ToSlash(rel), isDir) {
				continue
			}
			if event.Op&fsnotify.Create != 0 && isDir {
				_ = addHistoryWatchDirs(s.root, event.Name, watcher, ignore)
			}
			s.recordPath(filepath.ToSlash(rel))
			resetTimer()
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			if err != nil {
				s.setError(generation, err)
				resetTimer()
			}
		case <-timerC:
			timerC = nil
			if settings.CaptureExternalValue() {
				_, _, _ = s.captureIfChanged(ctx, historyOptions(settings, "external_change", "External workspace changes", ""))
				s.prune(settings)
			}
		case <-ticker.C:
			if settings.CaptureExternalValue() {
				_, _, _ = s.captureIfChanged(ctx, historyOptions(settings, "reconcile", "Periodic reconciliation", ""))
				s.prune(settings)
			}
		}
	}
}

func addHistoryWatchDirs(workspaceRoot, start string, watcher *fsnotify.Watcher, ignore func(string, bool) bool) error {
	return filepath.WalkDir(start, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(workspaceRoot, path)
		if path != workspaceRoot && ignore(filepath.ToSlash(rel), true) {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

func (s *LocalHistoryService) recordPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suppress {
		return
	}
	s.pending[path] = true
}

func (s *LocalHistoryService) captureIfChanged(ctx context.Context, opts localhistory.SnapshotOptions) (localhistory.Manifest, bool, error) {
	s.mu.Lock()
	if s.suppress {
		s.mu.Unlock()
		return localhistory.Manifest{}, false, nil
	}
	s.capturing = true
	s.mu.Unlock()
	manifest, changed, err := s.store.SnapshotIfChanged(ctx, opts)
	s.mu.Lock()
	s.capturing = false
	if err != nil {
		s.lastErr = err
	} else {
		s.lastErr = nil
		if changed {
			s.pending = make(map[string]bool)
		}
	}
	bus := s.bus
	s.mu.Unlock()
	if changed && bus != nil {
		bus.Send(HistorySnapshotCreated{Snapshot: localhistory.SnapshotRecord{ID: manifest.ID, ParentID: manifest.ParentID, CreatedAt: manifest.CreatedAt, Kind: manifest.Kind, Label: manifest.Label}})
	}
	return manifest, changed, err
}

func (s *LocalHistoryService) setError(generation uint64, err error) {
	s.mu.Lock()
	if generation == s.generation {
		s.lastErr = err
	}
	bus := s.bus
	s.mu.Unlock()
	if bus != nil {
		bus.Send(HistoryServiceStatus{Error: err.Error()})
	}
}

func (s *LocalHistoryService) prune(settings ttypes.LocalHistorySettings) {
	_, _ = s.store.Prune(context.Background(), localhistory.RetentionPolicy{
		KeepHours: settings.RetentionKeepHours, KeepDaily: settings.RetentionKeepDaily,
		MaxSnapshots: settings.RetentionMaxSnapshots, BudgetBytes: int64(settings.StorageBudgetMB) * 1024 * 1024,
	})
}

func scopeKey(sessionID, messageID string) string { return sessionID + "\x00" + messageID }

func (s *LocalHistoryService) BeginMessage(ctx context.Context, sessionID, messageID string) error {
	s.mu.Lock()
	settings := s.settings
	enabled := settings.EnabledValue()
	s.mu.Unlock()
	if !enabled {
		return session.ErrHistoryDisabled
	}
	s.activityMu.RLock()
	manifest, _, err := s.captureIfChanged(ctx, historyOptions(settings, "before_message", "Before message "+messageID, ""))
	if err != nil {
		s.activityMu.RUnlock()
		return err
	}
	if err := s.store.BeginMessageScope(sessionID, messageID, manifest.ID, time.Now()); err != nil {
		s.activityMu.RUnlock()
		return err
	}
	s.mu.Lock()
	s.active[scopeKey(sessionID, messageID)] = &historyScopeState{sessionID: sessionID, messageID: messageID, beforeID: manifest.ID, collisions: make(map[string]bool)}
	s.mu.Unlock()
	return nil
}

func (s *LocalHistoryService) EndMessage(ctx context.Context, sessionID, messageID string) error {
	key := scopeKey(sessionID, messageID)
	s.mu.Lock()
	scope := s.active[key]
	settings := s.settings
	s.mu.Unlock()
	if scope == nil {
		return nil
	}
	defer s.activityMu.RUnlock()
	manifest, _, err := s.captureIfChanged(ctx, historyOptions(settings, "after_message", "After message "+messageID, scope.beforeID))
	if err != nil {
		return err
	}
	changed, err := s.store.DiffSnapshotPaths(scope.beforeID, manifest.ID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if len(s.active) > 1 {
		for _, path := range changed {
			scope.collisions[path] = true
			for otherKey, other := range s.active {
				if otherKey != key {
					other.collisions[path] = true
				}
			}
		}
	}
	collisions := mapKeys(scope.collisions)
	s.mu.Unlock()
	if err := s.store.CloseMessageScope(sessionID, messageID, manifest.ID, changed, collisions, time.Now()); err != nil {
		return err
	}
	s.mu.Lock()
	if s.active[key] == scope {
		delete(s.active, key)
	}
	s.mu.Unlock()
	return nil
}

func (s *LocalHistoryService) BeginContinuation(_ context.Context, _, _ string) error {
	s.activityMu.RLock()
	s.mu.Lock()
	stopping := s.stopping
	s.mu.Unlock()
	if stopping {
		s.activityMu.RUnlock()
		return errors.New("local history service is stopping")
	}
	return nil
}

func (s *LocalHistoryService) EndContinuation(_ context.Context, _, _ string) error {
	s.activityMu.RUnlock()
	return nil
}

// RecordAgentMutation persists the direct tool's exact operation receipt before
// the tool mutates the workspace. Failure aborts the tool rather than allowing
// an unrewindable write.
func (s *LocalHistoryService) RecordAgentMutation(ctx context.Context, mutation session.HistoryMutation) error {
	if mutation.SessionID == "" || mutation.MessageID == "" || mutation.AgentID == "" || mutation.Receipt.OperationID == "" || mutation.Receipt.OperationName == "" {
		return errors.New("local history agent mutation is incomplete")
	}
	if mutation.Committed != nil {
		return s.store.CompleteAgentOperation(mutation.Receipt.OperationID, *mutation.Committed)
	}
	operation := localhistory.AgentOperation{
		ID: mutation.Receipt.OperationID, SessionID: mutation.SessionID, MessageID: mutation.MessageID,
		AgentID: mutation.AgentID, Source: mutation.Receipt.OperationName,
		Paths: make([]localhistory.OperationPath, 0, len(mutation.Receipt.Paths)),
	}
	for index, receiptPath := range mutation.Receipt.Paths {
		preserveLeaf := false
		if mutation.Receipt.OperationName == "move_file" && index == len(mutation.Receipt.Paths)-1 && index > 0 {
			source := mutation.Receipt.Paths[index-1].Path
			preserveLeaf = source != receiptPath.Path && strings.EqualFold(source, receiptPath.Path)
		}
		rel, err := s.operationRelativePath(receiptPath.Path, preserveLeaf)
		if err != nil {
			return err
		}
		before, err := operationState(receiptPath.Before)
		if err != nil {
			return err
		}
		after, err := operationState(receiptPath.After)
		if err != nil {
			return err
		}
		operation.Paths = append(operation.Paths, localhistory.OperationPath{Path: rel, Before: before, After: after})
	}
	return s.store.RecordAgentOperation(ctx, operation)
}

func (s *LocalHistoryService) operationRelativePath(path string, preserveLeaf bool) (string, error) {
	if path == "" {
		return "", errors.New("local history operation path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root := s.root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if preserveLeaf {
		parent, err := resolveOperationParent(filepath.Dir(abs))
		if err != nil {
			return "", fmt.Errorf("resolve operation parent: %w", err)
		}
		abs = filepath.Join(parent, filepath.Base(abs))
	} else if canonical, canonicalErr := canonicalExistingPath(abs); canonicalErr == nil {
		abs, err = filepath.EvalSymlinks(canonical)
		if err != nil {
			return "", err
		}
	} else {
		parent, err := resolveOperationParent(filepath.Dir(abs))
		if err != nil {
			return "", fmt.Errorf("resolve operation parent: %w", err)
		}
		abs = filepath.Join(parent, filepath.Base(abs))
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("agent mutation path escapes workspace: %s", path)
	}
	return filepath.ToSlash(rel), nil
}

func canonicalExistingPath(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path, nil
	}
	canonicalParent, err := canonicalExistingPath(parent)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(canonicalParent)
	if err != nil {
		return "", err
	}
	base := filepath.Base(path)
	for _, entry := range entries {
		if entry.Name() != base && !strings.EqualFold(entry.Name(), base) {
			continue
		}
		candidate := filepath.Join(canonicalParent, entry.Name())
		candidateInfo, err := os.Lstat(candidate)
		if err == nil && os.SameFile(info, candidateInfo) {
			return candidate, nil
		}
	}
	return path, nil
}

func resolveOperationParent(parent string) (string, error) {
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		base := filepath.Base(parent)
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		missing = append(missing, base)
		parent = next
	}
}

func operationState(state fileio.MutationState) (*localhistory.OperationState, error) {
	switch state.Kind {
	case fileio.MutationAbsent:
		return nil, nil
	case fileio.MutationRegular:
		return &localhistory.OperationState{Type: localhistory.EntryFile, Mode: uint32(state.Mode), Data: append([]byte(nil), state.Bytes...)}, nil
	case fileio.MutationDirectory:
		return &localhistory.OperationState{Type: localhistory.EntryDirectory, Mode: uint32(state.Mode)}, nil
	default:
		return nil, fmt.Errorf("unsupported mutation state %q", state.Kind)
	}
}

func mapKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (s *LocalHistoryService) LatestUndoable(sessionID string) (localhistory.MessageScope, bool, error) {
	return s.store.LatestUndoableScope(sessionID)
}
func (s *LocalHistoryService) UndoableScopes(sessionID string) ([]localhistory.MessageScope, error) {
	return s.store.UndoableScopes(sessionID, 100)
}
func (s *LocalHistoryService) AgentUndoableScopes(sessionID, agentID string) ([]localhistory.MessageScope, error) {
	return s.store.AgentUndoableScopes(sessionID, agentID, 100)
}
func (s *LocalHistoryService) PendingRestoreNotifications(sessionID string) ([]localhistory.RestoreResult, error) {
	return s.store.PendingRestoreNotifications(sessionID)
}
func (s *LocalHistoryService) MarkRestoreNotified(planID string) error {
	return s.store.MarkRestoreNotified(planID)
}
func (s *LocalHistoryService) MessageScope(sessionID, messageID string) (localhistory.MessageScope, bool, error) {
	return s.store.MessageScope(sessionID, messageID)
}
func (s *LocalHistoryService) PlanMessageRestore(ctx context.Context, sessionID, messageID string) (localhistory.RestorePlan, error) {
	s.mu.Lock()
	settings := s.settings
	s.mu.Unlock()
	return s.store.PlanMessageRestore(ctx, sessionID, messageID, historyOptions(settings, "", "", ""))
}
func (s *LocalHistoryService) PlanAgentRestore(ctx context.Context, sessionID, messageID, agentID string) (localhistory.RestorePlan, error) {
	s.mu.Lock()
	settings := s.settings
	s.mu.Unlock()
	return s.store.PlanAgentRestore(ctx, sessionID, messageID, agentID, historyOptions(settings, "", "", ""))
}

func (s *LocalHistoryService) ApplyRestore(ctx context.Context, planID string, selected []string) (localhistory.RestoreResult, error) {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return localhistory.RestoreResult{}, errors.New("local history service is stopping")
	}
	if s.restoring {
		s.mu.Unlock()
		return localhistory.RestoreResult{}, errors.New("local history restore already in progress")
	}
	if len(s.active) > 0 {
		s.mu.Unlock()
		return localhistory.RestoreResult{}, errors.New("local history message scope is still active")
	}
	s.opWg.Add(1)
	s.restoring = true
	s.suppress = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.suppress = false
		s.restoring = false
		s.pending = make(map[string]bool)
		s.mu.Unlock()
		s.opWg.Done()
	}()
	return s.store.ApplyRestore(ctx, planID, selected)
}

func (s *LocalHistoryService) ApplyRestoreChoices(ctx context.Context, planID string, choices []localhistory.RestoreChoice) (localhistory.RestoreResult, error) {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return localhistory.RestoreResult{}, errors.New("local history service is stopping")
	}
	if s.restoring {
		s.mu.Unlock()
		return localhistory.RestoreResult{}, errors.New("local history restore already in progress")
	}
	if len(s.active) > 0 {
		s.mu.Unlock()
		return localhistory.RestoreResult{}, errors.New("local history message scope is still active")
	}
	s.opWg.Add(1)
	s.restoring = true
	s.suppress = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.suppress = false
		s.restoring = false
		s.pending = make(map[string]bool)
		s.mu.Unlock()
		s.opWg.Done()
	}()
	return s.store.ApplyRestoreChoices(ctx, planID, choices)
}

func (s *LocalHistoryService) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restoring || s.capturing || len(s.active) > 0
}

func (w *Workspace) bindSessionHistory(backend *session.Session) {
	if backend == nil {
		return
	}
	if w.HistoryService == nil {
		openErr := w.localHistoryErr
		backend.SetHistoryHooks(func(context.Context, string, string) error {
			if !w.EffectiveAppSettings().LocalHistory.EnabledValue() {
				return session.ErrHistoryDisabled
			}
			if openErr != nil {
				return fmt.Errorf("local history is required but unavailable: %w", openErr)
			}
			return errors.New("local history is required but unavailable")
		}, nil)
		return
	}
	backend.SetHistoryHooks(w.HistoryService.BeginMessage, w.HistoryService.EndMessage)
	backend.SetHistoryContinuationHooks(w.HistoryService.BeginContinuation, w.HistoryService.EndContinuation)
	backend.SetHistoryMutationHook(w.HistoryService.RecordAgentMutation)
	backend.SetBeforeTurnHook(func(_ context.Context, sessionID string) error {
		return w.retryPendingHistoryNotifications(sessionID)
	})
}
