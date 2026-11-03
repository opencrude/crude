package core

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"crude/coreprotocol"
	"github.com/fsnotify/fsnotify"
)

const coreWatchQueueSize = 1024

type watchNotification struct {
	path string
	kind int
}

// coreFileWatch is retained once per workspace, independently of shell count.
// Its ordered notification queue is independent of actor and shell delivery.
type coreFileWatch struct {
	cancel               context.CancelFunc
	done, ready, lspDone chan struct{}
	once                 sync.Once
}

// notify must honor cancellation; reliable must synchronously mark coverage stale
// and request producer recovery. true means observation is restored; it does
// not mean producer results are fresh.
func startCoreFileWatch(root string, publish func(coreprotocol.FileChange), notify func(context.Context, string, int), reliable func(bool)) (*coreFileWatch, error) {
	// Fence existing and newly admitted project producers before starting a
	// worker or attempting directory admission.
	if reliable != nil {
		reliable(false)
	}
	root = filepath.Clean(root)
	watcher, err := newCoreWatcher(root)
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
		defer func() { _ = watcher.Close() }()
		generation, cancelGeneration := context.WithCancel(ctx)
		defer func() { cancelGeneration() }()
		admissionFailed := false
		var rootIdentity os.FileInfo
		gap := func() {
			// Cancellation is visible under the manager lock before a new
			// producer can be admitted, including callbacks already dequeued.
			cancelGeneration()
			if reliable != nil {
				reliable(false)
			}
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
			current, err := os.Lstat(root)
			if err != nil || rootIdentity == nil || !current.IsDir() || !os.SameFile(rootIdentity, current) {
				admissionFailed = true
			}
			if !admissionFailed && ctx.Err() == nil && reliable != nil {
				reliable(true)
			}
		}
		dirs := make(map[string]bool)
		add := func(path string) bool {
			info, err := os.Lstat(path)
			if err != nil || !info.IsDir() {
				return false
			}
			err = filepath.WalkDir(path, func(path string, entry fs.DirEntry, err error) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err != nil {
					return err
				}
				if !entry.IsDir() {
					return nil
				}
				if skipCoreWatchPath(root, path) {
					return filepath.SkipDir
				}
				if err := watcher.Add(path); err != nil {
					return err
				}
				dirs[coreWatchPathKey(path)] = true
				return nil
			})
			return err == nil
		}
		start := time.Now()
		// The initial traversal cannot observe files in directories not yet
		// admitted. Fence producers until the entire traversal completes.
		if ctx.Err() == nil && publish != nil {
			publish(coreprotocol.FileChange{Resync: true})
		}
		rootIdentity, _ = os.Lstat(root)
		if !addCoreWatchParent(watcher, root) {
			admissionFailed = true
			gap()
		}
		if !add(root) && ctx.Err() == nil {
			admissionFailed = true
			gap()
		}
		recoverObservation()
		fileWatchStart.Since(start)
		close(w.ready)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					if ctx.Err() == nil {
						gap()
					}
					return
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
				if event.Op&fsnotify.Create != 0 {
					info, err := os.Lstat(path)
					if err == nil && info.IsDir() {
						if !git {
							gap()
						}
						if !add(path) && ctx.Err() == nil {
							admissionFailed = true
							gap()
						}
						if !git {
							recoverObservation()
						}
					} else if err != nil && !os.IsNotExist(err) {
						admissionFailed = true
						gap()
					}
				}
				if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 && dirs[key] {
					if !git {
						gap()
					}
					// A replacement is not admitted reliably. Stay fenced until restart.
					if key == coreWatchPathKey(root) {
						admissionFailed = true
					}
					for dir := range dirs {
						if dir == key || strings.HasPrefix(dir, key+string(filepath.Separator)) {
							delete(dirs, dir)
						}
					}
					if !git {
						recoverObservation()
					}
				}
				if !git && notify != nil {
					kind := 2
					if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
						kind = 3
					} else if event.Op&fsnotify.Create != 0 {
						kind = 1
					}
					select {
					case queue <- queuedNotification{watchNotification{path, kind}, generation}:
					default:
						gap()
						recoverObservation()
					}
				}
				if ctx.Err() == nil && publish != nil {
					publish(coreprotocol.FileChange{Path: path, Removed: event.Op&(fsnotify.Remove|fsnotify.Rename) != 0, Git: git})
				}
			case watchErr, ok := <-watcher.Errors:
				if ctx.Err() == nil {
					gap()
					if ok && watchErr == fsnotify.ErrEventOverflow {
						if !add(root) {
							admissionFailed = true
						}
						recoverObservation()
					} else {
						admissionFailed = true
					}
				}
				if !ok {
					return
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
		w.once.Do(func() { w.cancel(); <-w.done; <-w.lspDone })
	}
}
