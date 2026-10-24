package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"unicode/utf8"

	"crude/coreprotocol"
	"crude/history"
	"crude/workspace"
)

type uploadOwner struct {
	owner     coreprotocol.Owner
	workspace string
}
type referenceUpload struct {
	owner uploadOwner
	input coreprotocol.ReferenceUploadCommand
	store workspace.SessionReferences
	file  *os.File
	next  int64
}

// ReferenceUploads is the sole staging lifetime owner. Every call performs I/O
// on a worker, not on the actor or UI. Declared-size quotas reserve disk before
// opening an upload: two uploads and 128 MiB per client, 256 MiB globally.
type ReferenceUploads struct {
	mu       sync.Mutex
	root     string
	open     map[string]*referenceUpload
	detached map[uploadOwner]bool
	closed   bool
}

func NewReferenceUploads(root string) *ReferenceUploads {
	return &ReferenceUploads{root: root, open: make(map[string]*referenceUpload), detached: make(map[uploadOwner]bool)}
}

func uploadMutation(method string) bool {
	switch method {
	case coreprotocol.ReferenceUploadBegin, coreprotocol.ReferenceUploadChunk, coreprotocol.ReferenceUploadCommit, coreprotocol.ReferenceUploadAbort:
		return true
	}
	return false
}

func (a *actor) prepareUpload(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	if !uploadMutation(request.Method) {
		return nil, false
	}
	var in coreprotocol.ReferenceUploadCommand
	if err := decodeRequest(request, &in); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	var store workspace.SessionReferences
	for _, owner := range a.ws.Sessions {
		if owner.SessionID == in.SessionID {
			store = owner.RefStore
			break
		}
	}
	if store == nil || a.runtime == nil || a.runtime.uploads == nil {
		return func(context.Context) coreprotocol.Response {
			return fault(request, "upload_unavailable", "exact session or upload owner is unavailable")
		}, true
	}
	manager := a.runtime.uploads
	return func(ctx context.Context) coreprotocol.Response {
		value, err := manager.run(ctx, request.Owner, request.WorkspaceID, store, request.Method, in)
		if err != nil {
			return fault(request, "upload_failed", err.Error())
		}
		return responseValue(request, value)
	}, true
}

func validDigest(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha256.Size && value == hex.EncodeToString(data)
}
func contentDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func (m *ReferenceUploads) remove(id string) {
	u := m.open[id]
	if u == nil {
		return
	}
	delete(m.open, id)
	_ = u.file.Close()
	_ = os.Remove(u.file.Name())
}

// Detach is also the admission fence for delayed workers. An old epoch cannot
// begin another upload after its existing files have been released.
func (m *ReferenceUploads) Detach(owner coreprotocol.Owner, workspace string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := uploadOwner{owner, workspace}
	m.detached[key] = true
	for id, u := range m.open {
		if u.owner == key {
			m.remove(id)
		}
	}
}

func (m *ReferenceUploads) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for id := range m.open {
		m.remove(id)
	}
	return nil
}

func (m *ReferenceUploads) run(ctx context.Context, owner coreprotocol.Owner, workspaceID string, store workspace.SessionReferences, method string, in coreprotocol.ReferenceUploadCommand) (coreprotocol.Value, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := uploadOwner{owner, workspaceID}
	if m.closed || m.detached[key] {
		return nil, errors.New("upload owner is detached")
	}
	if err := ctx.Err(); err != nil {
		if u := m.open[in.UploadID]; u != nil && u.owner == key {
			m.remove(in.UploadID)
		}
		return nil, err
	}
	if in.SessionID == "" {
		return nil, errors.New("session is required")
	}
	if method == coreprotocol.ReferenceUploadBegin {
		if in.UploadID != "" || len(in.Data) != 0 || in.Offset != 0 || in.Size < 0 || in.Size > coreprotocol.ReferenceMaxBytes || !validDigest(in.Digest) {
			return nil, errors.New("invalid upload size or digest")
		}
		count, reserved, total := 0, int64(0), int64(0)
		for _, u := range m.open {
			total += u.input.Size
			if u.owner.owner == owner {
				count++
				reserved += u.input.Size
			}
		}
		if count >= 2 || reserved+in.Size > 128<<20 || total+in.Size > 256<<20 {
			return nil, errors.New("upload quota is occupied")
		}
		switch in.Purpose {
		case coreprotocol.ReferenceUploadCreate:
			if in.Create == nil || len(in.Create.Data) != 0 || in.Create.ServerPath != "" || in.ID != "" || in.Revision != "" || in.OriginalDigest != "" {
				return nil, errors.New("invalid upload creation metadata")
			}
			copy := *in.Create
			in.Create = &copy
		case coreprotocol.ReferenceUploadReplace:
			if in.Create != nil || !validDigest(in.OriginalDigest) || in.Size > coreprotocol.ReferenceUploadMaxBytes {
				return nil, errors.New("exact replacement preimage is required")
			}
			ref, ok := store.GetReference(in.ID)
			if !ok || !ref.Active() || ref.SessionID != in.SessionID || in.Revision == "" || in.Revision != entityRevision(ref) {
				return nil, errors.New("replacement reference changed")
			}
		default:
			return nil, errors.New("invalid upload purpose")
		}
		if m.root == "" {
			return nil, errors.New("upload staging root is required")
		}
		if err := os.MkdirAll(m.root, 0700); err != nil {
			return nil, err
		}
		file, err := os.CreateTemp(m.root, ".reference-upload-*")
		if err != nil {
			return nil, err
		}
		id := NewClientID()
		m.open[id] = &referenceUpload{owner: key, input: in, store: store, file: file}
		return coreprotocol.ReferenceUploadResult{UploadID: id}, nil
	}
	u := m.open[in.UploadID]
	if u == nil || u.owner != key || u.input.SessionID != in.SessionID || u.store != store {
		return nil, errors.New("upload identity is stale or foreign")
	}
	if in.Purpose != "" || in.Create != nil || in.ID != "" || in.Revision != "" || in.OriginalDigest != "" || in.Size != 0 || in.Digest != "" {
		return nil, errors.New("upload metadata cannot change")
	}
	switch method {
	case coreprotocol.ReferenceUploadAbort:
		m.remove(in.UploadID)
		return wire(coreprotocol.Empty{}), nil
	case coreprotocol.ReferenceUploadChunk:
		if len(in.Data) == 0 || len(in.Data) > coreprotocol.ReferenceChunkBytes || in.Offset != u.next || u.next+int64(len(in.Data)) > u.input.Size {
			return nil, errors.New("invalid chunk offset or size")
		}
		n, err := u.file.Write(in.Data)
		if err != nil || n != len(in.Data) {
			m.remove(in.UploadID)
			return nil, errors.New("staging write failed")
		}
		u.next += int64(n)
		return coreprotocol.ReferenceUploadResult{UploadID: in.UploadID, Next: u.next}, nil
	case coreprotocol.ReferenceUploadCommit:
		if len(in.Data) != 0 || in.Offset != 0 || u.next != u.input.Size {
			return nil, errors.New("upload is incomplete")
		}
		defer m.remove(in.UploadID)
		if err := u.file.Sync(); err != nil {
			return nil, err
		}
		data, err := history.ReadAttachment(u.file.Name())
		if err != nil {
			return nil, err
		}
		if contentDigest(data) != u.input.Digest {
			return nil, errors.New("upload digest mismatch")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if u.input.Purpose == coreprotocol.ReferenceUploadCreate {
			req := *u.input.Create
			req.Data = data
			return runReferenceCommand(store, coreprotocol.ReferenceCreate, coreprotocol.ReferenceCommand{SessionID: in.SessionID, Create: &req})
		}
		referenceCommandMu.Lock()
		defer referenceCommandMu.Unlock()
		ref, ok := store.GetReference(u.input.ID)
		if !ok || !ref.Active() || ref.SessionID != in.SessionID || entityRevision(ref) != u.input.Revision {
			return nil, errors.New("replacement reference changed")
		}
		original, readOnly, err := store.ReadReferenceContent(ref.ID)
		if err != nil {
			return nil, err
		}
		if readOnly || contentDigest([]byte(original)) != u.input.OriginalDigest || !utf8.Valid(data) {
			return nil, errors.New("replacement preimage changed or content is not editable UTF-8")
		}
		if err := store.SaveReferenceContent(ref, original, string(data)); err != nil {
			return nil, err
		}
		updated, _ := store.GetReference(ref.ID)
		return wire(referenceView(updated)), nil
	}
	return nil, errors.New("unknown upload operation")
}
