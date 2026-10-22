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

// coreFileWatch is retained once per workspace, independently of shell count.
// Startup traversal and directory admission run on its worker, not the actor.
type coreFileWatch struct {
	cancel context.CancelFunc
	done   chan struct{}
	ready  chan struct{}
	once   sync.Once
}

func startCoreFileWatch(root string, publish func(coreprotocol.FileChange)) (*coreFileWatch, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &coreFileWatch{cancel: cancel, done: make(chan struct{}), ready: make(chan struct{})}
	root = filepath.Clean(root)
	go func() {
		defer close(w.done)
		defer func() { _ = watcher.Close() }()
		emit := func(change coreprotocol.FileChange) {
			if ctx.Err() == nil && publish != nil {
				publish(change)
			}
		}
		add := func(path string) {
			info, err := os.Lstat(path)
			if err != nil || !info.IsDir() {
				return
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
				return watcher.Add(path)
			})
			if err != nil && ctx.Err() == nil {
				emit(coreprotocol.FileChange{Resync: true})
			}
		}
		watchStart := time.Now()
		add(root)
		fileWatchStart.Since(watchStart)
		close(w.ready)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if skipCoreWatchPath(root, event.Name) {
					continue
				}
				fileEvents.Inc()
				if event.Op&fsnotify.Create != 0 {
					add(event.Name)
				}
				rel, _ := filepath.Rel(root, event.Name)
				emit(coreprotocol.FileChange{Path: filepath.Clean(event.Name), Removed: event.Op&(fsnotify.Remove|fsnotify.Rename) != 0, Git: rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator))})
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
				emit(coreprotocol.FileChange{Resync: true})
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
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		if part == ".claude" && i+1 < len(parts) && parts[i+1] == "worktrees" {
			return true
		}
	}
	return false
}

// Close cancels traversal and joins the worker. publish must use the actor's
// stop-aware ingress so shutdown cannot wait on a stopped actor.
func (w *coreFileWatch) Close() {
	if w != nil {
		w.once.Do(func() { w.cancel(); <-w.done })
	}
}
