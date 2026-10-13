package coreclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"crude/coreprotocol"
	"crude/history"
	"crude/sessiondb"
	"crude/workspace"
)

// referenceCallTimeout bounds one reference request; content transfers are
// chunked, so this covers the slowest acceptable 64 MiB exchange.
const referenceCallTimeout = 2 * time.Minute

// References is a session's reference replica. Reads are local; every write is
// a core request whose exact result is installed. AddExisting installs a remote
// result; it never writes an attachment, allocates a label, or persists.
type References struct {
	mu          sync.RWMutex
	refs        map[string]sessiondb.Reference
	revisions   map[string]string
	client      *Client
	workspaceID string
	sessionID   string
}

func NewReferences() *References {
	return &References{refs: make(map[string]sessiondb.Reference), revisions: make(map[string]string)}
}

func newSessionReferences(view *WorkspaceView, sessionID string) *References {
	r := NewReferences()
	r.client, r.workspaceID, r.sessionID = view.Client, view.ID, sessionID
	return r
}

var _ workspace.SessionReferences = (*References)(nil)

var errReferencesDetached = errors.New("references are not attached to a core session")

func (r *References) call() (*WorkspaceView, string, error) {
	if r == nil {
		return nil, "", errReferencesDetached
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.client == nil || r.workspaceID == "" || r.sessionID == "" {
		return nil, "", errReferencesDetached
	}
	return &WorkspaceView{Client: r.client, ID: r.workspaceID}, r.sessionID, nil
}

func (r *References) bind(view *WorkspaceView) {
	r.mu.Lock()
	r.client, r.workspaceID = view.Client, view.ID
	r.mu.Unlock()
}

// adopt installs a core result together with the revision fencing later writes.
func (r *References) adopt(view coreprotocol.ReferenceView) sessiondb.Reference {
	r.mu.Lock()
	defer r.mu.Unlock()
	if view.Reference.ID != "" {
		r.refs[view.Reference.ID] = view.Reference
		r.revisions[view.Reference.ID] = view.Revision
	}
	return view.Reference
}

// current returns the exact revisioned view a mutation must name. An unknown
// revision is fetched from the core rather than guessed.
func (r *References) current(ctx context.Context, id string) (coreprotocol.ReferenceView, error) {
	r.mu.RLock()
	ref, ok := r.refs[id]
	revision := r.revisions[id]
	r.mu.RUnlock()
	if ok && revision != "" {
		return coreprotocol.ReferenceView{Reference: ref, Revision: revision}, nil
	}
	view, session, err := r.call()
	if err != nil {
		return coreprotocol.ReferenceView{}, err
	}
	list, err := view.ListReferences(ctx, session)
	if err != nil {
		return coreprotocol.ReferenceView{}, err
	}
	var found *coreprotocol.ReferenceView
	for i := range list.References {
		r.adopt(list.References[i])
		if list.References[i].Reference.ID == id {
			found = &list.References[i]
		}
	}
	if found == nil {
		return coreprotocol.ReferenceView{}, fmt.Errorf("reference %s not found", id)
	}
	return *found, nil
}

func (r *References) CreateReference(req sessiondb.CreateRequest) (sessiondb.Reference, error) {
	view, session, err := r.call()
	if err != nil {
		return sessiondb.Reference{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), referenceCallTimeout)
	defer cancel()
	create := coreprotocol.ReferenceCreateRequest{Kind: req.Kind, Label: req.Label, Title: req.Title, Mime: req.Mime, Inline: req.Inline}
	// A path names a file on this client's machine; its bytes travel in frames.
	data := []byte(req.Content)
	if req.Kind == sessiondb.KindPastedImage {
		if req.Content != "" {
			return sessiondb.Reference{}, errors.New("image creation requires binary Data, not text Content")
		}
		data = req.Data
	}
	if req.FilePath != "" {
		if data, err = os.ReadFile(req.FilePath); err != nil {
			return sessiondb.Reference{}, err
		}
	}
	if len(data) > coreprotocol.ReferenceMaxBytes {
		return sessiondb.Reference{}, fmt.Errorf("reference exceeds %d bytes", coreprotocol.ReferenceMaxBytes)
	}
	if len(data) <= coreprotocol.ReferenceUploadMaxBytes {
		create.Data = data
		result, err := view.CreateReference(ctx, session, create)
		if err != nil {
			return sessiondb.Reference{}, err
		}
		return r.adopt(result), nil
	}
	digest := sha256.Sum256(data)
	result, err := view.Client.UploadReference(ctx, view.ID, coreprotocol.ReferenceUploadCommand{
		SessionID: session, Purpose: coreprotocol.ReferenceUploadCreate, Create: &create,
		Size: int64(len(data)), Digest: hex.EncodeToString(digest[:]),
	}, bytes.NewReader(data))
	if err != nil {
		return sessiondb.Reference{}, err
	}
	return r.adopt(result), nil
}

func (r *References) ReadReferenceContent(id string) (string, bool, error) {
	view, session, err := r.call()
	if err != nil {
		return "", false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), referenceCallTimeout)
	defer cancel()
	result, err := view.ReadReferenceContent(ctx, session, id)
	if err != nil {
		return "", false, err
	}
	r.adopt(result.View)
	return string(result.Data), result.ReadOnly, nil
}

func (r *References) SaveReferenceContent(expected sessiondb.Reference, original, content string) error {
	view, session, err := r.call()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), referenceCallTimeout)
	defer cancel()
	current, err := r.current(ctx, expected.ID)
	if err != nil {
		return err
	}
	if current.Reference != expected {
		return errors.New("reference changed since it was opened")
	}
	result, err := view.SaveReferenceContent(ctx, session, current, original, content)
	if err != nil {
		return err
	}
	r.adopt(result)
	return nil
}

func (r *References) ImportReference(old sessiondb.Reference) (sessiondb.Reference, error) {
	view, session, err := r.call()
	if err != nil {
		return sessiondb.Reference{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), referenceCallTimeout)
	defer cancel()
	current, err := r.current(ctx, old.ID)
	if err != nil {
		return sessiondb.Reference{}, err
	}
	result, err := view.ImportReference(ctx, session, current)
	if err != nil {
		return sessiondb.Reference{}, err
	}
	return r.adopt(result), nil
}

// RestorePrompt imports a prompt-history entry's attachments into this session.
// The core owns prompt history, so the entry is named by its exact identity.
func (r *References) RestorePrompt(entry history.Entry) (string, []string, error) {
	view, session, err := r.call()
	if err != nil {
		return "", nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), referenceCallTimeout)
	defer cancel()
	result, err := view.RestorePromptHistory(ctx, session, entry.ID)
	if err != nil {
		return "", nil, err
	}
	for _, ref := range result.References {
		r.adopt(ref)
	}
	return result.Text, result.ReferenceIDs, nil
}

func (r *References) DeleteReference(id string) error {
	view, session, err := r.call()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), referenceCallTimeout)
	defer cancel()
	current, err := r.current(ctx, id)
	if err != nil {
		return err
	}
	if err := view.DeleteReference(ctx, session, current); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.refs, id)
	delete(r.revisions, id)
	r.mu.Unlock()
	return nil
}

func (r *References) RenameReference(id, label string) (string, bool) {
	view, session, err := r.call()
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), referenceCallTimeout)
	defer cancel()
	current, err := r.current(ctx, id)
	if err != nil {
		return "", false
	}
	result, err := view.RenameReference(ctx, session, current, label)
	if err != nil {
		return "", false
	}
	return r.adopt(result).DisplayName(), true
}

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
	previousRefs, previousRevisions := r.refs, r.revisions
	r.refs = make(map[string]sessiondb.Reference, len(refs))
	r.revisions = make(map[string]string, len(refs))
	for _, ref := range refs {
		if ref.ID != "" {
			r.refs[ref.ID] = ref
			// A snapshot carries no revision; keep one only for an unchanged entity.
			if old, ok := previousRefs[ref.ID]; ok && old == ref {
				r.revisions[ref.ID] = previousRevisions[ref.ID]
			}
		}
	}
}
