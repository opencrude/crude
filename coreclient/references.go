package coreclient

import (
	"sort"
	"sync"

	"crude/sessiondb"
)

// References is a detached, read-through publication cache. AddExisting installs
// a remote result; it never writes an attachment, allocates a label, or persists.
type References struct {
	mu   sync.RWMutex
	refs map[string]sessiondb.Reference
}

func NewReferences() *References { return &References{refs: make(map[string]sessiondb.Reference)} }

func (r *References) GetReference(id string) (sessiondb.Reference, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ref, ok := r.refs[id]
	return ref, ok
}

func (r *References) ListReferences() []sessiondb.Reference {
	r.mu.RLock()
	defer r.mu.RUnlock()
	refs := make([]sessiondb.Reference, 0, len(r.refs))
	for _, ref := range r.refs {
		if ref.Active() {
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].CreatedAt != refs[j].CreatedAt {
			return refs[i].CreatedAt < refs[j].CreatedAt
		}
		return refs[i].ID < refs[j].ID
	})
	return refs
}

func (r *References) AddExisting(ref sessiondb.Reference) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refs == nil {
		r.refs = make(map[string]sessiondb.Reference)
	}
	if ref.ID != "" {
		r.refs[ref.ID] = ref
	}
}

func (r *References) install(refs []sessiondb.Reference) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refs = make(map[string]sessiondb.Reference, len(refs))
	for _, ref := range refs {
		if ref.ID != "" {
			r.refs[ref.ID] = ref
		}
	}
}
