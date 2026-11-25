package core

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"crude/core/coreprotocol"
	"crude/insight/textindex"
	"crude/internal/retry"
)

// directoryScope observes only loaded directory listings. Each shell retains
// its cache (including collapsed rows) until it detaches. No read or handle
// is needed for any other subtree. Open documents have their own disk watcher.
type directoryScope struct {
	mu       sync.Mutex
	owners   map[coreprotocol.Owner]map[string]bool
	requests chan directoryInterest
	prune    chan struct{}
	promoted chan struct{}
	done     <-chan struct{}
}
type directoryInterest struct {
	owner coreprotocol.Owner
	path  string
	reply chan error
}

func (s *directoryScope) attach(owner coreprotocol.Owner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners[owner] == nil {
		s.owners[owner] = make(map[string]bool)
	}
}
func (s *directoryScope) detach(owner coreprotocol.Owner) {
	s.mu.Lock()
	delete(s.owners, owner)
	s.mu.Unlock()
	select {
	case s.prune <- struct{}{}:
	default:
	}
}

// observe runs on the directory read worker. Install the watch before the
// listing so an outside edit cannot fall between the read and observation.
func (s *directoryScope) observe(ctx context.Context, owner coreprotocol.Owner, path string) error {
	req := directoryInterest{owner, path, make(chan error, 1)}
	select {
	case <-s.promoted:
		return nil
	case <-s.done:
		return context.Canceled
	case <-ctx.Done():
		return ctx.Err()
	case s.requests <- req:
	}
	select {
	case <-s.done:
		return context.Canceled
	case <-ctx.Done():
		return ctx.Err()
	case err := <-req.reply:
		return err
	}
}

// startDirectoryScope keeps a top-level workspace off the recursive watch and
// text index until a consumer needs project-wide coverage. The two modes never
// publish concurrently. Full coverage uses the normal gap/recovery owner.
func startDirectoryScope(root string, publish func(coreprotocol.FileChange), notify func(context.Context, string, int), reliable func(bool), analysis <-chan struct{}, index *textindex.Index) (*coreFileWatch, error) {
	native, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	rootInfo, err := os.Stat(root)
	if err == nil {
		err = native.Add(root)
	}
	if err != nil {
		_ = native.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &coreFileWatch{cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), lspDone: make(chan struct{})}
	close(w.lspDone)
	s := &directoryScope{owners: make(map[coreprotocol.Owner]map[string]bool), requests: make(chan directoryInterest), prune: make(chan struct{}, 1), promoted: make(chan struct{}), done: w.done}
	w.scope = s
	go func() {
		defer close(w.done)
		defer cancel()
		defer func() {
			if native != nil {
				_ = native.Close()
			}
		}()
		close(w.ready)
		paths := map[string]bool{root: true}
		query := index.Demand()
		var whole *coreFileWatch
		defer func() { whole.Close() }()
		var complete atomic.Bool
		var retryAt <-chan time.Time
		var timer *time.Timer
		var attempts int
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		recovery := func(ok bool) {
			complete.Store(ok)
			reliable(ok)
			if ok && index != nil {
				select {
				case <-index.Demand():
					index.Start()
				default:
				}
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case req := <-s.requests:
				s.mu.Lock()
				lease := s.owners[req.owner]
				s.mu.Unlock()
				if lease == nil {
					req.reply <- context.Canceled
					continue
				}
				if !paths[req.path] {
					if err := native.Add(req.path); err != nil {
						req.reply <- err
						continue
					}
					paths[req.path] = true
				}
				s.mu.Lock()
				if s.owners[req.owner] != nil {
					lease[req.path] = true
				}
				s.mu.Unlock()
				req.reply <- nil
			case <-s.prune:
				s.mu.Lock()
				keep := map[string]bool{root: true}
				for _, dirs := range s.owners {
					for path := range dirs {
						keep[path] = true
					}
				}
				s.mu.Unlock()
				for path := range paths {
					if !keep[path] {
						_ = native.Remove(path)
						delete(paths, path)
					}
				}
			case evt, ok := <-native.Events:
				if !ok {
					return
				}
				path := filepath.Clean(evt.Name)
				removed := evt.Has(fsnotify.Remove) || evt.Has(fsnotify.Rename)
				if removed {
					delete(paths, path)
				}
				if skipCoreWatchPath(root, path) {
					continue
				}
				rel, _ := filepath.Rel(root, path)
				git := rel == ".git" || filepath.Dir(rel) == ".git"
				publish(coreprotocol.FileChange{Path: path, Removed: removed, Git: git})
			case _, ok := <-native.Errors:
				if !ok {
					return
				}
				publish(coreprotocol.FileChange{Resync: true})
			case <-retryAt:
				retryAt = nil
				goto promote
			case <-analysis:
				analysis = nil
				goto promote
			case <-query:
				query = nil
				goto promote
			}
			continue
		promote:
			current, statErr := os.Stat(root)
			if statErr != nil || !os.SameFile(rootInfo, current) {
				publish(coreprotocol.FileChange{Resync: true})
				continue
			}
			// Start the complete observer before releasing the loaded folders.
			// It publishes a resync before the first traversal and fences LSP
			// until the entire root has been admitted.
			whole, err = startCoreFileWatch(root, publish, notify, recovery)
			if err != nil {
				attempts++
				if timer != nil {
					timer.Stop()
				}
				timer = time.NewTimer(retry.Exponential(attempts, 250*time.Millisecond, 30*time.Second))
				retryAt = timer.C
				publish(coreprotocol.FileChange{Resync: true})
				continue
			}
			close(s.promoted)
			_ = native.Close()
			native = nil
			if query != nil {
				select {
				case <-ctx.Done():
					return
				case <-query:
					if complete.Load() {
						index.Start()
					}
				}
			}
			<-ctx.Done()
			return
		}
	}()
	return w, nil
}
