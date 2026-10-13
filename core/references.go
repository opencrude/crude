package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"crude/coreprotocol"
	"crude/history"
	"crude/provider"
	"crude/sessiondb"
	"crude/workspace"
)

// Bridge mutations serialize against other bridge operations for this store.
// The actor must keep the captured session alive until its worker is joined.
var referenceCommandMu sync.Mutex

func referenceMutation(method string) bool {
	switch method {
	case coreprotocol.ReferenceCreate, coreprotocol.ReferenceImport, coreprotocol.ReferenceSaveContent, coreprotocol.ReferenceRename, coreprotocol.ReferenceDelete, coreprotocol.HistoryRecord, coreprotocol.HistoryRestore:
		return true
	}
	return false
}

func referenceView(ref sessiondb.Reference) coreprotocol.ReferenceView {
	return coreprotocol.ReferenceView{Reference: ref, Revision: entityRevision(ref)}
}

// prepareReferences resolves the exact session on the actor, never on a worker.
// Application history needs one injected global owner plus its persistence path;
// a client-local or workspace-local history writer is deliberately not invented.
func (a *actor) prepareReferences(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	switch request.Method {
	case coreprotocol.HistoryList, coreprotocol.HistoryRecord, coreprotocol.HistoryRestore, coreprotocol.HistoryReadMedia:
		return a.prepareHistory(request), true
	case coreprotocol.ReferenceCreate, coreprotocol.ReferenceImport, coreprotocol.ReferenceReadContent, coreprotocol.ReferenceReadMedia, coreprotocol.ReferenceSaveContent, coreprotocol.ReferenceRename, coreprotocol.ReferenceDelete, coreprotocol.ReferenceList:
	default:
		return nil, false
	}
	var input coreprotocol.ReferenceCommand
	if err := decode(request.Payload, &input); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	var owner *workspace.Session
	for _, candidate := range a.ws.Sessions {
		if candidate.SessionID == input.SessionID {
			owner = candidate
			break
		}
	}
	if input.SessionID == "" || owner == nil || owner.RefStore == nil {
		return func(context.Context) coreprotocol.Response {
			return fault(request, "session_missing", "exact reference session is not open")
		}, true
	}
	store := owner.RefStore
	return func(ctx context.Context) coreprotocol.Response {
		if err := ctx.Err(); err != nil {
			return fault(request, "cancelled", err.Error())
		}
		value, err := runReferenceCommand(store, request.Method, input)
		if err != nil {
			return fault(request, "reference_failed", err.Error())
		}
		return response(request, value)
	}, true
}

func runReferenceCommand(store workspace.SessionReferences, method string, in coreprotocol.ReferenceCommand) (any, error) {
	referenceCommandMu.Lock()
	defer referenceCommandMu.Unlock()
	if method == coreprotocol.ReferenceList {
		out := coreprotocol.ReferenceListResult{References: []coreprotocol.ReferenceView{}}
		for _, ref := range store.ListReferences() {
			out.References = append(out.References, referenceView(ref))
		}
		sort.Slice(out.References, func(i, j int) bool {
			return out.References[i].Reference.CreatedAt < out.References[j].Reference.CreatedAt
		})
		return out, nil
	}
	if method == coreprotocol.ReferenceCreate {
		if in.Create == nil {
			return nil, errors.New("reference creation data is required")
		}
		req := *in.Create
		if len(req.Data) > coreprotocol.ReferenceMaxBytes {
			return nil, errors.New("upload exceeds 64 MiB bound")
		}
		if len(req.Data) > coreprotocol.ReferenceUploadMaxBytes && (req.Kind == "" || req.Kind == sessiondb.KindPastedText || req.Inline) {
			return nil, errors.New("large text must use a file-backed reference")
		}
		if req.Label != "" && (!strings.HasPrefix(req.Label, "[") || !strings.HasSuffix(req.Label, "]") || strings.ContainsAny(req.Label, `/\\`)) {
			return nil, errors.New("invalid reference label")
		}
		if req.ServerPath != "" && (req.Kind != sessiondb.KindSelectedFile || len(req.Data) != 0) {
			return nil, errors.New("server path is only valid for selected-file references without uploaded bytes")
		}
		content := string(req.Data)
		var imageData []byte
		switch req.Kind {
		case "", sessiondb.KindPastedText, sessiondb.KindPastedTextFile:
			if !utf8.Valid(req.Data) {
				return nil, errors.New("text must be valid UTF-8")
			}
		case sessiondb.KindPastedImage:
			if len(req.Data) == 0 || !strings.HasPrefix(req.Mime, "image/") {
				return nil, errors.New("image requires explicit bytes and image MIME type")
			}
			if err := provider.ValidateImageBytes(req.Mime, req.Data); err != nil {
				return nil, err
			}
			content, imageData = "", req.Data
		case sessiondb.KindSelectedFile:
			if req.ServerPath == "" {
				return nil, errors.New("selected file requires a server path")
			}
		default:
			return nil, errors.New("reference kind is not supported for creation")
		}
		ref, err := store.CreateReference(sessiondb.CreateRequest{Kind: req.Kind, Label: req.Label, Title: req.Title, Mime: req.Mime, Content: content, Data: imageData, FilePath: req.ServerPath, Inline: req.Inline})
		return referenceView(ref), err
	}
	ref, ok := store.GetReference(in.ID)
	if !ok || !ref.Active() || ref.SessionID != in.SessionID {
		return nil, errors.New("reference is missing, inactive, or belongs to another session")
	}
	if in.Revision != "" && in.Revision != entityRevision(ref) {
		return nil, errors.New("reference changed since it was opened")
	}
	if referenceMutation(method) && in.Revision == "" {
		return nil, errors.New("exact reference revision is required")
	}
	switch method {
	case coreprotocol.ReferenceImport:
		created, err := store.ImportReference(ref)
		return referenceView(created), err
	case coreprotocol.ReferenceReadContent, coreprotocol.ReferenceReadMedia:
		var data []byte
		var readOnly bool
		var err error
		if method == coreprotocol.ReferenceReadMedia {
			readOnly = true
			if ref.FilePath == "" {
				return nil, errors.New("reference has no media file")
			}
			data, err = history.ReadAttachment(ref.FilePath)
		} else {
			var text string
			text, readOnly, err = store.ReadReferenceContent(ref.ID)
			data = []byte(text)
		}
		if err != nil {
			return nil, err
		}
		if len(data) > coreprotocol.ReferenceMaxBytes {
			return nil, errors.New("content exceeds 64 MiB read bound")
		}
		return referenceChunk(referenceView(ref), data, readOnly, in.Offset, in.Digest)
	case coreprotocol.ReferenceSaveContent:
		if len(in.Content) > coreprotocol.ReferenceUploadMaxBytes || len(in.Original) > coreprotocol.ReferenceUploadMaxBytes {
			return nil, errors.New("save exceeds inline transport bound; chunked upload is not offered")
		}
		if err := store.SaveReferenceContent(ref, in.Original, in.Content); err != nil {
			return nil, err
		}
		updated, _ := store.GetReference(ref.ID)
		return referenceView(updated), nil
	case coreprotocol.ReferenceRename:
		label, ok := store.RenameReference(ref.ID, in.Label)
		if !ok {
			return nil, errors.New("reference rename failed")
		}
		ref.Label = label
		return referenceView(ref), nil
	case coreprotocol.ReferenceDelete:
		return struct{}{}, store.DeleteReference(ref.ID)
	}
	return nil, errors.New("unknown reference operation")
}

func referenceChunk(view coreprotocol.ReferenceView, data []byte, readOnly bool, offset int64, expected string) (coreprotocol.ReferenceContentResult, error) {
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	if len(data) > coreprotocol.ReferenceMaxBytes || offset < 0 || offset > int64(len(data)) || offset != 0 && expected == "" || expected != "" && expected != digest {
		return coreprotocol.ReferenceContentResult{}, errors.New("invalid offset or content changed during download")
	}
	end := min(int64(len(data)), offset+coreprotocol.ReferenceChunkBytes)
	return coreprotocol.ReferenceContentResult{View: view, Data: data[offset:end], ReadOnly: readOnly, Total: int64(len(data)), Next: end, Digest: digest}, nil
}
