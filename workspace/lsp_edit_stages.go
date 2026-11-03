package workspace

import (
	"context"
	"path/filepath"
	"sync"
)

// editStageRegistry records only exact paths reserved by the reviewed-edit
// writer. It is not a filename-prefix exclusion. Event order retires an entry
// on removal/rename, so a later file at the same path is a project change.
// Eviction can cause an unnecessary review refusal, never an ignored change.
type editStageRegistry struct {
	mu      sync.Mutex
	entries map[string]*editStage
	next    uint64
	gap     bool
}

type editStage struct {
	ready    chan struct{}
	resolved bool
	owned    bool
	order    uint64
}

const maxEditStages = 1024

func (r *editStageRegistry) reserve(path string) *editStage {
	path = filepath.Clean(path)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gap {
		return nil
	}
	if r.entries == nil {
		r.entries = make(map[string]*editStage)
	}
	if r.entries[path] != nil {
		return nil
	}
	if len(r.entries) >= maxEditStages {
		var oldest string
		var order uint64
		for candidate, stage := range r.entries {
			if stage.resolved && (oldest == "" || stage.order < order) {
				oldest, order = candidate, stage.order
			}
		}
		if oldest == "" {
			return nil
		}
		delete(r.entries, oldest)
	}
	r.next++
	stage := &editStage{ready: make(chan struct{}), order: r.next}
	r.entries[path] = stage
	return stage
}

// A lost observation may have lost a terminal removal event. Retire every
// reservation and reject new ones until observation returns. This can cause a
// conservative refusal, but an old reservation cannot hide a later real file.
func (r *editStageRegistry) setReliable(reliable bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gap = !reliable
	if !reliable {
		clear(r.entries)
	}
}

func (r *editStageRegistry) resolve(path string, stage *editStage, owned bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stage.owned, stage.resolved = owned, true
	if !owned && r.entries[filepath.Clean(path)] == stage {
		delete(r.entries, filepath.Clean(path))
	}
	close(stage.ready)
}

func (r *editStageRegistry) ownsEvent(ctx context.Context, path string, changeType int) bool {
	path = filepath.Clean(path)
	r.mu.Lock()
	stage := r.entries[path]
	r.mu.Unlock()
	if stage == nil {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	case <-stage.ready:
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil || r.entries[path] != stage || !stage.owned {
		return false
	}
	if changeType == 3 {
		delete(r.entries, path)
	}
	return true
}
