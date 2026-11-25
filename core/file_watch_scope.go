package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
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
	root     watchRoot
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
		return s.root.check()
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

// Keep the loaded listings live until the recursive observer has installed
// its watches on every readable directory. Resync once at the handoff.
func (f fileWatchFeed) startScope(pin watchRoot, analysis <-chan struct{}, index *textindex.Index) (*coreFileWatch, error) {
	root, publish := pin.path, f.publish
	native, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	err = native.Add(root)
	if err == nil {
		err = pin.check()
	}
	if err != nil {
		_ = native.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &coreFileWatch{cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}), lspDone: make(chan struct{})}
	close(w.lspDone)
	s := &directoryScope{root: pin, owners: make(map[coreprotocol.Owner]map[string]bool), requests: make(chan directoryInterest), prune: make(chan struct{}, 1), promoted: make(chan struct{}), done: w.done}
	w.scope = s
	go func() {
		defer close(w.done)
		defer func() {
			if native != nil {
				_ = native.Close()
			}
		}()
		close(w.ready)
		paths := map[string]bool{root: true}
		query := index.Demand()
		var whole *coreFileWatch
		defer func() { cancel(); whole.Close() }()
		// Coverage reports are synchronous with the walk. The worker cannot
		// send ordinary changes before the scope has finished the handoff.
		reports := make(chan watchCoverage)
		ack := make(chan struct{})
		var usable bool
		events, watchErrors := native.Events, native.Errors
		requests, prune := s.requests, s.prune
		var retryAt <-chan time.Time
		var timer *time.Timer
		var attempts int
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		candidate := f
		candidate.coverage = func(c watchCoverage) {
			select {
			case reports <- c:
			case <-ctx.Done():
				return
			}
			select {
			case <-ack:
			case <-ctx.Done():
			}
		}
		lost := func() { f.coverage(watchCoverage{rootLost: true}); publish(coreprotocol.FileChange{Resync: true}) }
		for {
			select {
			case <-ctx.Done():
				return
			case c := <-reports:
				if c.rootLost {
					lost()
					return
				}
				if c.usable && native != nil {
					_ = native.Close()
					if err := pin.check(); err != nil {
						lost()
						return
					}
					native = nil
					events, watchErrors = nil, nil
					requests, prune = nil, nil
					close(s.promoted)
					publish(coreprotocol.FileChange{Resync: true})
				}
				usable = c.usable
				f.coverage(c)
				if usable && query == nil && index != nil {
					index.Start()
				}
				select {
				case ack <- struct{}{}:
				case <-ctx.Done():
					return
				}
			case req := <-requests:
				if err := pin.check(); err != nil {
					req.reply <- err
					continue
				}
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
			case <-prune:
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
			case evt, ok := <-events:
				if !ok {
					return
				}
				path := filepath.Clean(evt.Name)
				removed := evt.Has(fsnotify.Remove) || evt.Has(fsnotify.Rename)
				if removed {
					if coreWatchPathKey(path) == coreWatchPathKey(root) {
						lost()
						return
					}
					delete(paths, path)
				}
				if skipCoreWatchPath(root, path) {
					continue
				}
				rel, _ := filepath.Rel(root, path)
				git := rel == ".git" || filepath.Dir(rel) == ".git"
				publish(coreprotocol.FileChange{Path: path, Removed: removed, Git: git})
			case _, ok := <-watchErrors:
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
				if usable && index != nil {
					if err := pin.check(); err != nil {
						lost()
						return
					}
					index.Start()
				}
				goto promote
			}
			continue
		promote:
			if whole != nil || retryAt != nil {
				continue
			}
			whole, err = candidate.startRecursive(pin)
			if err != nil {
				if os.IsNotExist(err) || errors.Is(err, errWatchRootChanged) {
					lost()
					return
				}
				attempts++
				if timer != nil {
					timer.Stop()
				}
				timer = time.NewTimer(retry.Exponential(attempts, 250*time.Millisecond, 30*time.Second))
				retryAt = timer.C
				publish(coreprotocol.FileChange{Resync: true})
				continue
			}
		}
	}()
	return w, nil
}
