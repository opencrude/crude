package core

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"crude/core/coreprotocol"
	"crude/insight/textindex"
	"crude/internal/retry"
	"github.com/fsnotify/fsnotify"
)

const coreWatchQueueSize = 1024

// coreWatchSubtreeLimit bounds what a new directory may already hold when its
// watch is added. Up to this many entries become ordinary create events; a
// larger subtree (an unpacked archive, a package install) is a gap and the
// producers reindex instead.
const coreWatchSubtreeLimit = coreWatchQueueSize / 2

type watchNotification struct {
	path string
	kind int
}

// coreFileWatch is retained once per workspace, independently of shell count.
// Its ordered notification queue is independent of actor and shell delivery.
type coreFileWatch struct {
	anchor               *os.File
	scope                *directoryScope
	cancel               context.CancelFunc
	done, ready, lspDone chan struct{}
	once                 sync.Once
}

type fileWatchFeed struct {
	publish  func(coreprotocol.FileChange)
	notify   func(context.Context, string, int)
	coverage func(watchCoverage)
}

func (f fileWatchFeed) start(path string, scoped bool, analysis <-chan struct{}, index *textindex.Index) (*coreFileWatch, error) {
	f.coverage(watchCoverage{})
	root, err := pinWatchRoot(path)
	if err != nil {
		return nil, err
	}
	var w *coreFileWatch
	if scoped {
		w, err = f.startScope(root, analysis, index)
	} else {
		w, err = f.startRecursive(root)
	}
	if err != nil {
		_ = root.handle.Close()
		return nil, err
	}
	w.anchor = root.handle
	return w, nil
}

// The scope and its replacement share the same pinned root. Coverage is
// reported before the worker accepts ordinary native events.
func (f fileWatchFeed) startRecursive(pin watchRoot) (*coreFileWatch, error) {
	root, publish, notify := pin.path, f.publish, f.notify
	if err := pin.check(); err != nil {
		return nil, err
	}
	watcher, err := newCoreWatcher(pin)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &coreFileWatch{cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), lspDone: make(chan struct{})}
	type queuedNotification struct {
		watchNotification
		ctx context.Context
	}
	queue := make(chan queuedNotification, coreWatchQueueSize)
	go func() {
		defer close(w.lspDone)
		for {
			select {
			case <-ctx.Done():
				return
			case item := <-queue:
				if ctx.Err() != nil {
					return
				}
				if notify != nil {
					notify(item.ctx, item.path, item.kind)
				}
			}
		}
	}()
	go func() {
		defer close(w.done)
		defer cancel()
		defer func() {
			if watcher != nil {
				_ = watcher.Close()
			}
		}()
		generation, cancelGeneration := context.WithCancel(ctx)
		defer func() { cancelGeneration() }()
		admissionFailed := false
		excluded := make(map[string]bool)
		rootLost := false
		admittedOnce := false
		events, watchErrors := watcher.Events, watcher.Errors
		var recoveryTimer *time.Timer
		var recoverAt <-chan time.Time
		recoveryAttempt := 0
		defer func() {
			if recoveryTimer != nil {
				recoveryTimer.Stop()
			}
		}()
		scheduleRecovery := func() {
			if rootLost || ctx.Err() != nil || recoverAt != nil {
				return
			}
			recoveryAttempt++
			recoveryTimer = time.NewTimer(retry.Exponential(recoveryAttempt, 250*time.Millisecond, 30*time.Second))
			recoverAt = recoveryTimer.C
		}
		gap := func() {
			// Cancellation is visible under the manager lock before a new
			// producer can be admitted, including callbacks already dequeued.
			cancelGeneration()
			f.coverage(watchCoverage{})
			generation, cancelGeneration = context.WithCancel(ctx)
			// The producer must reindex instead of consuming a partial queue.
			for len(queue) > 0 {
				select {
				case <-queue:
				default:
				}
			}
			if ctx.Err() == nil && publish != nil {
				publish(coreprotocol.FileChange{Resync: true})
			}
		}
		recoverObservation := func() {
			if err := pin.check(); err != nil {
				admissionFailed = true
				rootLost = rootLost || os.IsNotExist(err) || errors.Is(err, errWatchRootChanged)
			}
			if rootLost {
				cancelGeneration()
				f.coverage(watchCoverage{rootLost: true})
				return
			}
			if admissionFailed {
				scheduleRecovery()
				return
			}
			var sample string
			for path := range excluded {
				if sample == "" || path < sample {
					sample = path
				}
			}
			if sample != "" {
				scheduleRecovery()
			} else {
				recoveryAttempt = 0
			}
			if ctx.Err() == nil {
				f.coverage(watchCoverage{usable: true, excluded: sample})
				admittedOnce = true
			}
		}
		dirs := make(map[string]bool)
		// add watches path and every directory below it. With found, it also
		// lists what the subtree already holds: entries created before their
		// directory had a watch produced no event, so they become create
		// events. complete is false when the subtree exceeds the bound.
		add := func(path string, found *[]watchNotification) (admitted, complete bool) {
			// A denied descendant is one missing branch, not a failed walk.
			// An entry that vanished before admission has nothing to watch.
			skipError := func(path string, entry fs.DirEntry, err error) error {
				if path == root || (!errors.Is(err, fs.ErrPermission) && !errors.Is(err, fs.ErrNotExist)) {
					return err
				}
				if errors.Is(err, fs.ErrPermission) {
					excluded[path] = true
				}
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			complete = true
			err := walkCoreWatch(path, func(entryPath string, entry fs.DirEntry, err error) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err != nil {
					return skipError(entryPath, entry, err)
				}
				if entryPath == root && !entry.IsDir() {
					return errWatchRootChanged
				}
				if skipCoreWatchPath(root, entryPath) {
					if entry.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if found != nil && entryPath != path {
					if len(*found) >= coreWatchSubtreeLimit {
						complete = false
					} else {
						*found = append(*found, watchNotification{entryPath, 1})
					}
				}
				if !entry.IsDir() {
					return nil
				}
				if err := watcher.Add(entryPath); err != nil {
					return skipError(entryPath, entry, err)
				}
				dirs[coreWatchPathKey(entryPath)] = true
				return nil
			})
			return err == nil, complete
		}
		start := time.Now()
		// The initial traversal cannot observe files in directories not yet
		// admitted. Fence producers until the entire traversal completes.
		if ctx.Err() == nil && publish != nil {
			publish(coreprotocol.FileChange{Resync: true})
		}

		if !addCoreWatchParent(watcher, root) {
			admissionFailed = true
			gap()
		}
		if admitted, _ := add(root, nil); !admitted && ctx.Err() == nil {
			admissionFailed = true
			gap()
		}
		recoverObservation()
		fileWatchStart.Since(start)
		close(w.ready)
		for !rootLost {
			select {
			case <-ctx.Done():
				return
			case <-recoverAt:
				recoverAt = nil
				// Never recover into another workspace at the same path.
				if err := pin.check(); err != nil {
					recoverObservation()
					continue
				}
				if !admissionFailed {
					// Retry only excluded branches. An unchanged denial does not
					// invalidate readable content or restart project producers.
					changed := false
					for path := range maps.Clone(excluded) {
						delete(excluded, path)
						admitted, _ := add(path, nil)
						if !admitted {
							admissionFailed = true
							break
						}
						changed = changed || !excluded[path]
					}
					if changed || admissionFailed {
						gap()
						recoverObservation()
					} else if len(excluded) != 0 {
						scheduleRecovery()
					}
					continue
				}
				// A backend failure, unlike a denied branch, needs a new observer.
				gap()
				events, watchErrors = nil, nil
				if watcher != nil {
					if closeErr := watcher.Close(); closeErr != nil {
						scheduleRecovery()
						continue
					}
					watcher = nil
				}
				watcher, err = newCoreWatcher(pin)
				if err != nil {
					scheduleRecovery()
					continue
				}
				events, watchErrors = watcher.Events, watcher.Errors
				dirs = make(map[string]bool)
				clear(excluded)
				admitted, _ := add(root, nil)
				admissionFailed = !addCoreWatchParent(watcher, root) || !admitted
				recoverObservation()
			case event, ok := <-events:
				if !ok {
					if ctx.Err() == nil {
						gap()
						admissionFailed = true
						events, watchErrors = nil, nil
						recoverObservation()
					}
					continue
				}
				path := filepath.Clean(event.Name)
				rel, err := filepath.Rel(root, path)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					continue
				}
				if skipCoreWatchPath(root, path) {
					continue
				}
				fileEvents.Inc()
				key := coreWatchPathKey(path)
				relKey := coreWatchPathKey(rel)
				git := relKey == ".git" || strings.HasPrefix(relKey, ".git"+string(filepath.Separator))
				// A new directory is observed without a gap: its watch is added
				// and what it already holds is reported as created, in order.
				// Builds, interpreters and package managers create directories
				// all the time; a gap would recheck the whole project and restart
				// its servers each time. Only lost events are a gap.
				var existing []watchNotification
				if event.Op&fsnotify.Create != 0 && key != coreWatchPathKey(root) {
					info, err := os.Lstat(path)
					if err == nil && info.IsDir() {
						var found *[]watchNotification
						if !git {
							found = &existing
						}
						previousExcluded := len(excluded)
						admitted, complete := add(path, found)
						switch {
						case ctx.Err() != nil:
						case !admitted:
							// A directory that vanished again holds nothing to observe.
							if _, err := os.Lstat(path); !os.IsNotExist(err) {
								admissionFailed = true
								gap()
								recoverObservation()
							}
						case !complete || len(excluded) != previousExcluded:
							existing = nil
							gap()
							recoverObservation()
						}
					} else if os.IsPermission(err) {
						if !excluded[path] {
							excluded[path] = true
							gap()
							recoverObservation()
						}
					} else if err != nil && !os.IsNotExist(err) {
						admissionFailed = true
						gap()
						recoverObservation()
					}
				}
				if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 && dirs[key] {
					// A removed directory is reported as one delete below. Only the
					// root itself cannot be readmitted: stay fenced until restart.
					if key == coreWatchPathKey(root) {
						gap()
						admissionFailed = true
						rootLost = true
						recoverObservation()
					}
					for dir := range dirs {
						if dir == key || strings.HasPrefix(dir, key+string(filepath.Separator)) {
							delete(dirs, dir)
						}
					}
				}
				// Directory metadata changes do not modify every source below it.
				if admittedOnce && !rootLost && !git && notify != nil && (!dirs[key] || event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0) {
					kind := 2
					if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
						kind = 3
					} else if event.Op&fsnotify.Create != 0 {
						kind = 1
					}
					for _, item := range append([]watchNotification{{path, kind}}, existing...) {
						select {
						case queue <- queuedNotification{item, generation}:
							continue
						default:
							gap()
							recoverObservation()
						}
						break
					}
				}
				if admittedOnce && ctx.Err() == nil && publish != nil {
					publish(coreprotocol.FileChange{Path: path, Removed: event.Op&(fsnotify.Remove|fsnotify.Rename) != 0, Git: git})
				}
			case watchErr, ok := <-watchErrors:
				if ctx.Err() == nil {
					gap()
					if ok && watchErr == fsnotify.ErrEventOverflow {
						clear(excluded)
						if admitted, _ := add(root, nil); !admitted {
							admissionFailed = true
						}
						recoverObservation()
					} else {
						admissionFailed = true
						events, watchErrors = nil, nil
						recoverObservation()
					}
				}
			}
		}
	}()
	return w, nil
}

func skipCoreWatchPath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	parts := strings.Split(coreWatchPathKey(rel), string(filepath.Separator))
	for i, part := range parts {
		if part == ".claude" && i+1 < len(parts) && parts[i+1] == "worktrees" {
			return true
		}
	}
	return false
}

// Close cancels traversal and joins both workers. Callbacks must cooperate.
func (w *coreFileWatch) Close() {
	if w != nil {
		w.once.Do(func() {
			w.cancel()
			<-w.done
			<-w.lspDone
			if w.anchor != nil {
				_ = w.anchor.Close()
			}
		})
	}
}
