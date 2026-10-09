package remote

import (
	"context"
	"crude/coreprotocol"
	"crude/document"
	"crude/shellport"
	"crude/workspace"
	"encoding/json"
	"errors"
)

var _ shellport.LiveDocuments = (*Workspace)(nil)

func (w *Workspace) DocumentOwner() document.Owner {
	client, _ := w.conn()
	if client == nil {
		return document.Owner{}
	}
	owner := client.Owner()
	return document.Owner{ClientID: owner.ClientID, Epoch: owner.Epoch}
}
func (w *Workspace) OpenDocument(ctx context.Context, path string) (document.Snapshot, error) {
	var out document.Snapshot
	err := w.call(ctx, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}, &out)
	return out, err
}
func (w *Workspace) SyncDocument(ctx context.Context, id string, after uint64) (document.SyncResult, error) {
	var out document.SyncResult
	err := w.call(ctx, coreprotocol.DocumentSync, coreprotocol.DocumentSyncRequest{ID: id, After: after}, &out)
	return out, err
}
func (w *Workspace) documentCall(ctx context.Context, requestID, method string, input, out any) error {
	client, id := w.conn()
	if client == nil {
		return errDisconnected
	}
	if requestID == "" {
		return document.ErrRequest
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	r, err := client.CallRequest(ctx, coreprotocol.Request{ID: requestID, Owner: client.Owner(), WorkspaceID: id, Method: method, Payload: payload})
	if err != nil {
		return err
	}
	if r.Error != nil {
		return r.Error
	}
	return json.Unmarshal(r.Payload, out)
}
func (w *Workspace) EditDocument(ctx context.Context, c document.Change) (document.Receipt, error) {
	var out document.Receipt
	if c.Owner != w.DocumentOwner() {
		return out, document.ErrOwner
	}
	err := w.documentCall(ctx, c.RequestID, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: c.DocumentID, Revision: c.BaseRevision, Group: c.GroupID, Edits: c.Edits}, &out)
	var failure *coreprotocol.Fault
	if errors.As(err, &failure) && failure.Code == "resource_failed" {
		return document.Receipt{RequestID: c.RequestID, DocumentID: c.DocumentID, Before: c.BaseRevision, Revision: c.BaseRevision, Error: failure.Message}, nil
	}
	return out, err
}
func (w *Workspace) UndoDocument(ctx context.Context, id, requestID string, revision uint64, redo bool) (document.Receipt, error) {
	var out document.Receipt
	method := coreprotocol.DocumentUndo
	if redo {
		method = coreprotocol.DocumentRedo
	}
	err := w.documentCall(ctx, requestID, method, coreprotocol.DocumentRevisionRequest{ID: id, Revision: revision}, &out)
	var failure *coreprotocol.Fault
	if errors.As(err, &failure) && failure.Code == "resource_failed" {
		return document.Receipt{RequestID: requestID, DocumentID: id, Before: revision, Revision: revision, Error: failure.Message}, nil
	}
	return out, err
}
func (w *Workspace) SaveDocument(ctx context.Context, id string, revision uint64, reason workspace.DocumentSaveReason) (document.Snapshot, error) {
	var out document.Snapshot
	err := w.call(ctx, coreprotocol.DocumentSave, coreprotocol.DocumentRevisionRequest{ID: id, Revision: revision, Reason: reason}, &out)
	return out, err
}
func (w *Workspace) CloseDocument(ctx context.Context, id string) error {
	return w.call(ctx, coreprotocol.DocumentClose, coreprotocol.DocumentReadRequest{ID: id}, nil)
}
func (w *Workspace) FormatDocument(ctx context.Context, id string, revision uint64) (document.Snapshot, error) {
	var out document.Snapshot
	err := w.documentCall(ctx, randomID(), coreprotocol.DocumentFormat, coreprotocol.DocumentRevisionRequest{ID: id, Revision: revision}, &out)
	return out, err
}

func (w *Workspace) SaveDocumentRequest(ctx context.Context, id string, revision uint64, reason workspace.DocumentSaveReason, requestID string) (document.Snapshot, error) {
	var out document.Snapshot
	err := w.documentCall(ctx, requestID, coreprotocol.DocumentSave, coreprotocol.DocumentRevisionRequest{ID: id, Revision: revision, Reason: reason}, &out)
	return out, err
}
func (w *Workspace) FormatDocumentRequest(ctx context.Context, id string, revision uint64, requestID string) (document.Snapshot, error) {
	var out document.Snapshot
	err := w.documentCall(ctx, requestID, coreprotocol.DocumentFormat, coreprotocol.DocumentRevisionRequest{ID: id, Revision: revision}, &out)
	return out, err
}
func (w *Workspace) DocumentSnapshotReceipt(ctx context.Context, requestID string) (document.Snapshot, bool, error) {
	var receipt coreprotocol.Receipt
	if err := w.call(ctx, coreprotocol.GetReceipt, coreprotocol.ReceiptRequest{RequestID: requestID}, &receipt); err != nil {
		return document.Snapshot{}, false, err
	}
	if receipt.State != "completed" {
		return document.Snapshot{}, false, nil
	}
	if receipt.WorkspaceID != w.id || receipt.RequestID != requestID || (receipt.Method != coreprotocol.DocumentSave && receipt.Method != coreprotocol.DocumentFormat) {
		return document.Snapshot{}, true, document.ErrRequest
	}
	if receipt.Response.Error != nil {
		return document.Snapshot{}, true, receipt.Response.Error
	}
	var out document.Snapshot
	err := json.Unmarshal(receipt.Response.Payload, &out)
	return out, true, err
}
func (w *Workspace) DocumentReceipt(ctx context.Context, requestID string) (document.Receipt, bool, error) {
	var receipt coreprotocol.Receipt
	err := w.call(ctx, coreprotocol.GetReceipt, coreprotocol.ReceiptRequest{RequestID: requestID}, &receipt)
	if err != nil {
		return document.Receipt{}, false, err
	}
	if receipt.State != "completed" {
		return document.Receipt{}, false, nil
	}
	if receipt.Response.Error != nil {
		return document.Receipt{RequestID: requestID, Error: receipt.Response.Error.Message}, true, nil
	}
	if receipt.Method != coreprotocol.DocumentEdit && receipt.Method != coreprotocol.DocumentUndo && receipt.Method != coreprotocol.DocumentRedo {
		return document.Receipt{}, true, errors.New("receipt is not a document edit")
	}
	var out document.Receipt
	err = json.Unmarshal(receipt.Response.Payload, &out)
	return out, true, err
}
