package coreclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"crude/coreprotocol"
)

func (v *WorkspaceView) CreateReference(ctx context.Context, sessionID string, input coreprotocol.ReferenceCreateRequest) (coreprotocol.ReferenceView, error) {
	var out coreprotocol.ReferenceView
	err := v.Client.Call(ctx, v.ID, coreprotocol.ReferenceCreate, coreprotocol.ReferenceCommand{SessionID: sessionID, Create: &input}, &out)
	return out, err
}
func (v *WorkspaceView) ListReferences(ctx context.Context, sessionID string) (coreprotocol.ReferenceListResult, error) {
	var out coreprotocol.ReferenceListResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.ReferenceList, coreprotocol.ReferenceCommand{SessionID: sessionID}, &out)
	return out, err
}
func (v *WorkspaceView) ImportReference(ctx context.Context, sessionID string, ref coreprotocol.ReferenceView) (coreprotocol.ReferenceView, error) {
	var out coreprotocol.ReferenceView
	err := v.Client.Call(ctx, v.ID, coreprotocol.ReferenceImport, coreprotocol.ReferenceCommand{SessionID: sessionID, ID: ref.Reference.ID, Revision: ref.Revision}, &out)
	return out, err
}
func (v *WorkspaceView) SaveReferenceContent(ctx context.Context, sessionID string, ref coreprotocol.ReferenceView, original, content string) (coreprotocol.ReferenceView, error) {
	var out coreprotocol.ReferenceView
	err := v.Client.Call(ctx, v.ID, coreprotocol.ReferenceSaveContent, coreprotocol.ReferenceCommand{SessionID: sessionID, ID: ref.Reference.ID, Revision: ref.Revision, Original: original, Content: content}, &out)
	return out, err
}
func (v *WorkspaceView) RenameReference(ctx context.Context, sessionID string, ref coreprotocol.ReferenceView, label string) (coreprotocol.ReferenceView, error) {
	var out coreprotocol.ReferenceView
	err := v.Client.Call(ctx, v.ID, coreprotocol.ReferenceRename, coreprotocol.ReferenceCommand{SessionID: sessionID, ID: ref.Reference.ID, Revision: ref.Revision, Label: label}, &out)
	return out, err
}
func (v *WorkspaceView) DeleteReference(ctx context.Context, sessionID string, ref coreprotocol.ReferenceView) error {
	return v.Client.Call(ctx, v.ID, coreprotocol.ReferenceDelete, coreprotocol.ReferenceCommand{SessionID: sessionID, ID: ref.Reference.ID, Revision: ref.Revision}, &struct{}{})
}
func (v *WorkspaceView) ReadReferenceContent(ctx context.Context, sessionID, id string) (coreprotocol.ReferenceContentResult, error) {
	return v.downloadReference(ctx, sessionID, id, coreprotocol.ReferenceReadContent)
}

// DownloadReferenceMedia returns verified bytes for a client-owned temporary file.
// Never pass the server metadata FilePath to a client desktop opener.
func (v *WorkspaceView) DownloadReferenceMedia(ctx context.Context, sessionID, id string) (coreprotocol.ReferenceContentResult, error) {
	return v.downloadReference(ctx, sessionID, id, coreprotocol.ReferenceReadMedia)
}
func (v *WorkspaceView) downloadReference(ctx context.Context, sessionID, id, method string) (coreprotocol.ReferenceContentResult, error) {
	in := coreprotocol.ReferenceCommand{SessionID: sessionID, ID: id}
	var out coreprotocol.ReferenceContentResult
	for {
		var chunk coreprotocol.ReferenceContentResult
		if err := v.Client.Call(ctx, v.ID, method, in, &chunk); err != nil {
			return out, err
		}
		if chunk.Total < 0 || chunk.Total > coreprotocol.ReferenceMaxBytes || chunk.Next != in.Offset+int64(len(chunk.Data)) || chunk.Next > chunk.Total || chunk.Next == in.Offset && chunk.Next != chunk.Total || in.Offset != 0 && (chunk.Digest != out.Digest || chunk.Total != out.Total || chunk.View.Revision != out.View.Revision) {
			return out, errors.New("invalid or changed reference download")
		}
		if in.Offset == 0 {
			out = chunk
			out.Data = nil
		}
		out.Data = append(out.Data, chunk.Data...)
		out.Next = chunk.Next
		if chunk.Next == chunk.Total {
			break
		}
		in.Offset, in.Digest, in.Revision = chunk.Next, chunk.Digest, chunk.View.Revision
	}
	hash := sha256.Sum256(out.Data)
	if hex.EncodeToString(hash[:]) != out.Digest {
		return out, errors.New("reference download digest mismatch")
	}
	return out, nil
}
func (v *WorkspaceView) ListPromptHistory(ctx context.Context, sessionID string) (coreprotocol.HistoryListResult, error) {
	var out coreprotocol.HistoryListResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.HistoryList, coreprotocol.HistoryCommand{SessionID: sessionID}, &out)
	return out, err
}
func (v *WorkspaceView) RecordPromptHistory(ctx context.Context, in coreprotocol.HistoryCommand) error {
	return v.Client.Call(ctx, v.ID, coreprotocol.HistoryRecord, in, &struct{}{})
}
func (v *WorkspaceView) RestorePromptHistory(ctx context.Context, sessionID, entryID string) (coreprotocol.HistoryRestoreResult, error) {
	var out coreprotocol.HistoryRestoreResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.HistoryRestore, coreprotocol.HistoryCommand{SessionID: sessionID, EntryID: entryID}, &out)
	return out, err
}
