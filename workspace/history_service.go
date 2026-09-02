package workspace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

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
	paths      map[string]bool
	collisions map[string]bool
}

type LocalHistoryService struct {
	root   string
	store  *localhistory.Store
	logger Logger

	mu         sync.Mutex
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
	if len(s.active) == 1 {
		for _, scope := range s.active {
			scope.paths[path] = true
		}
	} else if len(s.active) > 1 {
		for _, scope := range s.active {
			scope.paths[path] = true
			scope.collisions[path] = true
		}
	}
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
		return nil
	}
	manifest, _, err := s.captureIfChanged(ctx, historyOptions(settings, "before_message", "Before message "+messageID, ""))
	if err != nil {
		return err
	}
	if err := s.store.BeginMessageScope(sessionID, messageID, manifest.ID, time.Now()); err != nil {
		return err
	}
	s.mu.Lock()
	s.active[scopeKey(sessionID, messageID)] = &historyScopeState{sessionID: sessionID, messageID: messageID, beforeID: manifest.ID, paths: make(map[string]bool), collisions: make(map[string]bool)}
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
	delete(s.active, key)
	s.mu.Unlock()
	return s.store.CloseMessageScope(sessionID, messageID, manifest.ID, changed, collisions, time.Now())
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
func (s *LocalHistoryService) MessageScope(sessionID, messageID string) (localhistory.MessageScope, bool, error) {
	return s.store.MessageScope(sessionID, messageID)
}
func (s *LocalHistoryService) PlanMessageRestore(ctx context.Context, sessionID, messageID string) (localhistory.RestorePlan, error) {
	s.mu.Lock()
	settings := s.settings
	s.mu.Unlock()
	return s.store.PlanMessageRestore(ctx, sessionID, messageID, historyOptions(settings, "", "", ""))
}

func (s *LocalHistoryService) ApplyRestore(ctx context.Context, planID string, selected []string) (localhistory.RestoreResult, error) {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return localhistory.RestoreResult{}, errors.New("local history service is stopping")
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
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return localhistory.RestoreResult{}, errors.New("local history service is stopping")
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
	if backend == nil || w.HistoryService == nil {
		return
	}
	backend.SetHistoryHooks(w.HistoryService.BeginMessage, w.HistoryService.EndMessage)
}
