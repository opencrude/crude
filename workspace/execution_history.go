package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"crude/localhistory"
)

type executionHistoryOwner struct {
	mu     sync.Mutex
	closed bool
	stores map[string]*localhistory.Store
}

var executionHistories sync.Map // *Workspace -> *executionHistoryOwner

// ExecutionHistory borrows the artifact identity; it never constructs a second
// Workspace. The recovery data lives beside, not inside, any checkout.
func (w *Workspace) ExecutionHistory(id string) (*localhistory.Store, string, error) {
	if w == nil || w.Executions == nil || w.WorkspaceDir == "" {
		return nil, "", errors.New("execution history authority unavailable")
	}
	c, err := w.Executions.Inspect(id)
	if err != nil || !c.Ready || c.Removed || c.LogicalRoot != w.RootPath {
		return nil, "", errors.New("execution history artifact unavailable or foreign")
	}
	if c.Root == w.RootPath {
		if w.LocalHistory == nil {
			return nil, "", errors.New("local history unavailable")
		}
		return w.LocalHistory, c.Root, nil
	}
	// Use artifact identity only as a map key; hash the directory component so
	// neither caller text nor future artifact formats can escape the data root.
	value, _ := executionHistories.LoadOrStore(w, &executionHistoryOwner{stores: map[string]*localhistory.Store{}})
	owner := value.(*executionHistoryOwner)
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closed {
		return nil, "", errors.New("execution history is closed")
	}
	if store := owner.stores[id]; store != nil {
		return store, c.Root, nil
	}
	path := filepath.Join(w.WorkspaceDir, "execution-history", fmt.Sprintf("%x", sha256.Sum256([]byte(c.ID+"\x00"+c.Root))))
	if documentUnder(c.Root, path) || documentUnder(w.RootPath, path) {
		return nil, "", errors.New("execution history must be outside all project checkouts")
	}
	store, err := localhistory.Open(path, c.Root)
	if err != nil {
		return nil, "", err
	}
	owner.stores[id] = store
	return store, c.Root, nil
}

// CloseExecutionHistory is called by Workspace shutdown before its borrowed
// services disappear. Stores retain their immutable data after artifact removal.
func CloseExecutionHistory(w *Workspace) error {
	value, _ := executionHistories.LoadOrStore(w, &executionHistoryOwner{stores: map[string]*localhistory.Store{}})
	owner := value.(*executionHistoryOwner)
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.closed = true
	var err error
	for id, store := range owner.stores {
		err = errors.Join(err, store.Close())
		delete(owner.stores, id)
	}
	return err
}

// BeginHistoryRestoreLease captures every current session on its owner thread.
// The core must separately deny creation of new sessions until release.
func (w *Workspace) BeginHistoryRestoreLease(ctx context.Context) (func(), error) {
	var releases []func()
	release := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, frontend := range w.Sessions {
		if frontend == nil || frontend.backend == nil {
			continue
		}
		unlock, err := frontend.backend.BeginHistoryRestoreLease(ctx)
		if err != nil {
			release()
			return nil, err
		}
		releases = append(releases, unlock)
	}
	var once sync.Once
	return func() { once.Do(release) }, nil
}
