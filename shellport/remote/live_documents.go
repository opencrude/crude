package remote

import (
	"context"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/document"
	"crude/shellport"
	"crude/workspace"
	"errors"
	"sync"
	"sync/atomic"
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
	if err == nil {
		w.registerDocumentTopic(out.ID, path)
	}
	return out, err
}
func (w *Workspace) SyncDocument(ctx context.Context, id string, after uint64) (document.SyncResult, error) {
	var out document.SyncResult
	err := w.call(ctx, coreprotocol.DocumentSync, coreprotocol.DocumentSyncRequest{ID: id, After: after}, &out)
	return out, err
}

// documentCall carries the editor's own request identity, so a retried edit
// returns its original receipt instead of applying twice.
func (w *Workspace) documentCall(ctx context.Context, requestID, method string, input coreprotocol.Value, out coreprotocol.Target) error {
	client, id := w.conn()
	if client == nil {
		return errDisconnected
	}
	if requestID == "" {
		return document.ErrRequest
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	_, err := client.CallWith(ctx, id, method, input, out, coreclient.CallOptions{ID: requestID})
	return err
}
func (w *Workspace) EditDocument(ctx context.Context, c document.Change) (document.Receipt, error) {
	var out document.Receipt
	if c.Owner != w.DocumentOwner() {
		return out, document.ErrOwner
	}
	err := w.documentCall(ctx, c.RequestID, coreprotocol.DocumentEdit, &coreprotocol.DocumentEditRequest{ID: c.DocumentID, Revision: c.BaseRevision, Group: c.GroupID, Edits: c.Edits}, &out)
	var failure *coreprotocol.Fault
	if errors.As(err, &failure) && failure.Code == "resource_failed" {
		if out.DocumentID == c.DocumentID && out.RequestID == c.RequestID {
			return out, nil
		}
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
	err := w.documentCall(ctx, requestID, method, &coreprotocol.DocumentRevisionRequest{ID: id, Revision: revision}, &out)
	var failure *coreprotocol.Fault
	if errors.As(err, &failure) && failure.Code == "resource_failed" {
		if out.DocumentID == id && out.RequestID == requestID {
			return out, nil
		}
		return document.Receipt{RequestID: requestID, DocumentID: id, Before: revision, Revision: revision, Error: failure.Message}, nil
	}
	return out, err
}
func (w *Workspace) SaveDocument(ctx context.Context, id string, revision uint64, reason workspace.DocumentSaveReason) (document.Snapshot, error) {
	var out document.Snapshot
	err := w.call(ctx, coreprotocol.DocumentSave, &coreprotocol.DocumentRevisionRequest{ID: id, Revision: revision, Reason: reason}, &out)
	return out, err
}
func (w *Workspace) CloseDocument(ctx context.Context, id string) error {
	w.liveDocumentMu.Lock()
	if topic := w.liveDocumentTopics[id]; topic != nil {
		topic.cancel()
		delete(w.liveDocumentTopics, id)
	}
	w.liveDocumentMu.Unlock()
	return w.call(ctx, coreprotocol.DocumentClose, coreprotocol.DocumentReadRequest{ID: id}, nil)
}

type liveDocumentTopic struct {
	path   string
	client *coreclient.Client
	cancel context.CancelFunc
	ended  atomic.Bool
}

func (w *Workspace) registerDocumentTopic(id, path string) {
	client, _ := w.conn()
	w.liveDocumentMu.Lock()
	defer w.liveDocumentMu.Unlock()
	if w.liveDocumentTopics == nil {
		w.liveDocumentTopics = make(map[string]*liveDocumentTopic)
	}
	if old := w.liveDocumentTopics[id]; old != nil && old.client == client {
		return
	}
	w.startDocumentTopicLocked(client, id, path, false)
}

func (w *Workspace) startDocumentTopics(client *coreclient.Client) {
	w.liveDocumentMu.Lock()
	defer w.liveDocumentMu.Unlock()
	for id, old := range w.liveDocumentTopics {
		if old.client != client {
			old.cancel()
			w.startDocumentTopicLocked(client, id, old.path, true)
		}
	}
}

func (w *Workspace) startDocumentTopicLocked(client *coreclient.Client, id, path string, reopen bool) {
	ctx, cancel := context.WithCancel(w.ctx)
	token := &liveDocumentTopic{path: path, client: client, cancel: cancel}
	w.liveDocumentTopics[id] = token
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer cancel()
		defer token.ended.Store(true)
		if reopen {
			var snapshot document.Snapshot
			_, workspaceID := w.conn()
			if err := client.Call(ctx, workspaceID, coreprotocol.DocumentOpen, &coreprotocol.DocumentOpenRequest{Path: path}, &snapshot); err != nil {
				w.reportFailure("Reopen document", err)
				return
			}
			if snapshot.ID != id {
				w.reportFailure("Reopen document", document.ErrOwner)
				return
			}
		}
		_, workspaceID := w.conn()
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			insight := client.Subscribe(ctx, workspaceID, coreprotocol.TopicInsight, id, func(_ coreprotocol.TopicHeader, body []byte) error {
				var update coreprotocol.InsightUpdate
				if err := coreprotocol.Decode(body, &update); err != nil {
					return err
				}
				done := make(chan error, 1)
				var once sync.Once
				w.send(shellport.DocumentInsightMsg{Owner: w, View: update.View,
					Valid: func() bool {
						w.liveDocumentMu.Lock()
						defer w.liveDocumentMu.Unlock()
						current, _ := w.conn()
						return ctx.Err() == nil && w.liveDocumentTopics[id] == token && current == client
					},
					Done: func(err error) { once.Do(func() { done <- err }) },
				})
				select {
				case err := <-done:
					return err
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			<-insight.Done()
		}()
		sub := client.Subscribe(ctx, workspaceID, coreprotocol.TopicDocument, id, func(header coreprotocol.TopicHeader, body []byte) error {
			var update coreprotocol.DocumentUpdate
			if err := coreprotocol.Decode(body, &update); err != nil {
				w.reportFailure("Decode document", err)
				return err
			}
			w.liveDocumentMu.Lock()
			if w.liveDocumentTopics[id] == token && update.State.Path != "" {
				token.path = update.State.Path
			}
			w.liveDocumentMu.Unlock()
			done := make(chan error, 1)
			var once sync.Once
			msg := shellport.LiveDocumentsChangedMsg{Owner: w, DocumentID: id, Header: header, Update: update,
				Valid: func() bool {
					w.liveDocumentMu.Lock()
					defer w.liveDocumentMu.Unlock()
					current, _ := w.conn()
					return ctx.Err() == nil && w.liveDocumentTopics[id] == token && current == client
				}, Done: func(err error) {
					once.Do(func() {
						if err != nil {
							token.ended.Store(true)
						}
						done <- err
					})
				}}
			w.send(msg)
			select {
			case err := <-done:
				if err != nil {
					token.ended.Store(true)
				}
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		<-sub.Done()
	}()
}
func (w *Workspace) FormatDocument(ctx context.Context, id string, revision uint64) (document.Snapshot, error) {
	var out document.Snapshot
	err := w.documentCall(ctx, randomID(), coreprotocol.DocumentFormat, &coreprotocol.DocumentRevisionRequest{ID: id, Revision: revision}, &out)
	return out, err
}

func (w *Workspace) SaveDocumentRequest(ctx context.Context, id string, revision uint64, reason workspace.DocumentSaveReason, requestID string) (document.Snapshot, error) {
	var out document.Snapshot
	err := w.documentCall(ctx, requestID, coreprotocol.DocumentSave, &coreprotocol.DocumentRevisionRequest{ID: id, Revision: revision, Reason: reason}, &out)
	return out, err
}
func (w *Workspace) FormatDocumentRequest(ctx context.Context, id string, revision uint64, requestID string) (document.Snapshot, error) {
	var out document.Snapshot
	err := w.documentCall(ctx, requestID, coreprotocol.DocumentFormat, &coreprotocol.DocumentRevisionRequest{ID: id, Revision: revision}, &out)
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
	err := coreprotocol.Decode(receipt.Response.Payload, &out)
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
	if receipt.RequestID != requestID || receipt.WorkspaceID != w.id || (receipt.Method != coreprotocol.DocumentEdit && receipt.Method != coreprotocol.DocumentUndo && receipt.Method != coreprotocol.DocumentRedo) {
		return document.Receipt{}, true, document.ErrRequest
	}
	if receipt.Response.Error != nil {
		var out document.Receipt
		if len(receipt.Response.Payload) != 0 && coreprotocol.Decode(receipt.Response.Payload, &out) == nil && out.RequestID == requestID {
			return out, true, nil
		}
		return document.Receipt{RequestID: requestID, Error: receipt.Response.Error.Message}, true, nil
	}
	if receipt.Method != coreprotocol.DocumentEdit && receipt.Method != coreprotocol.DocumentUndo && receipt.Method != coreprotocol.DocumentRedo {
		return document.Receipt{}, true, errors.New("receipt is not a document edit")
	}
	var out document.Receipt
	err = coreprotocol.Decode(receipt.Response.Payload, &out)
	return out, true, err
}

func (w *Workspace) FenceDocumentTopics() {
	w.liveDocumentMu.Lock()
	defer w.liveDocumentMu.Unlock()
	for _, topic := range w.liveDocumentTopics {
		topic.ended.Store(true)
		topic.cancel()
	}
}

func (w *Workspace) ResumeDocumentTopics() {
	client, _ := w.conn()
	w.liveDocumentMu.Lock()
	defer w.liveDocumentMu.Unlock()
	if w.ctx.Err() != nil {
		return
	}
	for id, topic := range w.liveDocumentTopics {
		if topic.ended.Load() {
			w.startDocumentTopicLocked(client, id, topic.path, false)
		}
	}
}
