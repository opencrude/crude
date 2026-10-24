package coreclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"crude/coreprotocol"
)

func (v *WorkspaceView) CreateReference(ctx context.Context, sessionID string, input coreprotocol.ReferenceCreateRequest) (coreprotocol.ReferenceView, error) {
	if len(input.Data) > 0 {
		hash := sha256.Sum256(input.Data)
		data := input.Data
		input.Data = nil
		return v.Client.UploadReference(ctx, v.ID, coreprotocol.ReferenceUploadCommand{SessionID: sessionID, Purpose: coreprotocol.ReferenceUploadCreate, Create: &input, Size: int64(len(data)), Digest: hex.EncodeToString(hash[:])}, bytes.NewReader(data))
	}
	var out coreprotocol.ReferenceView
	err := v.Client.Call(ctx, v.ID, coreprotocol.ReferenceCreate, &coreprotocol.ReferenceCommand{SessionID: sessionID, Create: &input}, &out)
	return out, err
}
func (v *WorkspaceView) ListReferences(ctx context.Context, sessionID string) (coreprotocol.ReferenceListResult, error) {
	var out coreprotocol.ReferenceListResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.ReferenceList, &coreprotocol.ReferenceCommand{SessionID: sessionID}, &out)
	return out, err
}
func (v *WorkspaceView) ImportReference(ctx context.Context, sessionID string, ref coreprotocol.ReferenceView) (coreprotocol.ReferenceView, error) {
	var out coreprotocol.ReferenceView
	err := v.Client.Call(ctx, v.ID, coreprotocol.ReferenceImport, &coreprotocol.ReferenceCommand{SessionID: sessionID, ID: ref.Reference.ID, Revision: ref.Revision}, &out)
	return out, err
}
func (v *WorkspaceView) SaveReferenceContent(ctx context.Context, sessionID string, ref coreprotocol.ReferenceView, original, content string) (coreprotocol.ReferenceView, error) {
	originalHash, contentHash := sha256.Sum256([]byte(original)), sha256.Sum256([]byte(content))
	return v.Client.UploadReference(ctx, v.ID, coreprotocol.ReferenceUploadCommand{SessionID: sessionID, Purpose: coreprotocol.ReferenceUploadReplace, ID: ref.Reference.ID, Revision: ref.Revision, OriginalDigest: hex.EncodeToString(originalHash[:]), Size: int64(len(content)), Digest: hex.EncodeToString(contentHash[:])}, bytes.NewBufferString(content))
}
func (v *WorkspaceView) RenameReference(ctx context.Context, sessionID string, ref coreprotocol.ReferenceView, label string) (coreprotocol.ReferenceView, error) {
	var out coreprotocol.ReferenceView
	err := v.Client.Call(ctx, v.ID, coreprotocol.ReferenceRename, &coreprotocol.ReferenceCommand{SessionID: sessionID, ID: ref.Reference.ID, Revision: ref.Revision, Label: label}, &out)
	return out, err
}
func (v *WorkspaceView) DeleteReference(ctx context.Context, sessionID string, ref coreprotocol.ReferenceView) error {
	return v.Client.Call(ctx, v.ID, coreprotocol.ReferenceDelete, &coreprotocol.ReferenceCommand{SessionID: sessionID, ID: ref.Reference.ID, Revision: ref.Revision}, &coreprotocol.Empty{})
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
	var out coreprotocol.ReferenceContentResult
	data, digest, err := v.Client.DownloadBulk(ctx, v.ID, method, &coreprotocol.ReferenceCommand{SessionID: sessionID, ID: id}, &out, coreprotocol.ReferenceMaxBytes)
	if err != nil {
		return coreprotocol.ReferenceContentResult{}, err
	}
	if out.Total != int64(len(data)) || out.Next != out.Total {
		return coreprotocol.ReferenceContentResult{}, errors.New("invalid reference metadata")
	}
	out.Data, out.Digest = data, digest
	return out, nil
}
func (v *WorkspaceView) ListPromptHistory(ctx context.Context, sessionID string) (coreprotocol.HistoryListResult, error) {
	var out coreprotocol.HistoryListResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.HistoryList, &coreprotocol.HistoryCommand{SessionID: sessionID}, &out)
	return out, err
}
func (v *WorkspaceView) RecordPromptHistory(ctx context.Context, in coreprotocol.HistoryCommand) error {
	return v.Client.Call(ctx, v.ID, coreprotocol.HistoryRecord, &in, &coreprotocol.Empty{})
}
func (v *WorkspaceView) RestorePromptHistory(ctx context.Context, sessionID, entryID string) (coreprotocol.HistoryRestoreResult, error) {
	var out coreprotocol.HistoryRestoreResult
	err := v.Client.Call(ctx, v.ID, coreprotocol.HistoryRestore, &coreprotocol.HistoryCommand{SessionID: sessionID, EntryID: entryID}, &out)
	return out, err
}
