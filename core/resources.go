package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/document"
	"crude/projectpath"
	"crude/terminalvt"
	"crude/workspace"
)

// prepareResource captures exact workspace-owned service lifetimes. A client
// never supplies an in-process owner, callback, service handle or database.
func (a *actor) prepareResource(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
	controller := workspace.TerminalController{ClientID: owner.ClientID, Epoch: owner.Epoch}
	docs, terminals, manager := a.ws.LiveDocuments, a.ws.Terminals, a.ws.TermMgr
	bad := func(err error) (func(context.Context) coreprotocol.Response, bool) {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	ok := func(value coreprotocol.Value, err error) coreprotocol.Response {
		if err != nil {
			return fault(request, "resource_failed", err.Error())
		}
		return responseValue(request, value)
	}
	switch request.Method {
	case coreprotocol.DocumentCompareUntitled:
		var input coreprotocol.ReviewedEditRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		return func(ctx context.Context) coreprotocol.Response {
			value, err := docs.ReadUntitledOverwrite(ctx, owner, input.ID, input.Digest)
			return ok(&coreprotocol.UntitledComparisonReply{Comparison: value}, err)
		}, true
	case coreprotocol.DocumentCreateUntitled:
		var input coreprotocol.Empty
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		encoding := a.ws.EffectiveAppSettings().Editor.NewFileEncoding
		return func(context.Context) coreprotocol.Response {
			value, err := docs.CreateUntitled(owner, encoding)
			return ok(wire(value), err)
		}, true
	case coreprotocol.DocumentOpenUntitled:
		var input coreprotocol.DocumentReadRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := docs.OpenUntitled(owner, input.ID)
			return ok(wire(value), err)
		}, true
	case coreprotocol.DocumentSaveUntitled, coreprotocol.DocumentReviewUntitled:
		var input coreprotocol.DocumentSaveUntitledRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		if err := projectpath.ValidateFileName(input.Name); err != nil {
			return bad(err)
		}
		if !filepath.IsAbs(input.Directory) || !filepath.IsLocal(input.Name) {
			return bad(fmt.Errorf("choose an absolute folder and a valid file name"))
		}
		if request.Method == coreprotocol.DocumentReviewUntitled {
			if a.ws.LocalHistory == nil || a.ws.FileOps == nil {
				return bad(errors.New("safe file replacement is unavailable"))
			}
			return func(ctx context.Context) coreprotocol.Response {
				value, err := docs.PrepareUntitledOverwrite(ctx, owner, input.ID, input.Revision, filepath.Join(input.Directory, input.Name), input.Encoding)
				if err != nil {
					return fault(request, "review_failed", err.Error())
				}
				return response(request, coreprotocol.ReviewedEditReply{Preview: value})
			}, true
		}
		return func(ctx context.Context) coreprotocol.Response {
			value, err := docs.SaveUntitled(ctx, owner, input.ID, input.Revision, filepath.Join(input.Directory, input.Name), input.Encoding)
			if errors.Is(err, workspace.ErrDocumentSaveUncertain) {
				return fault(request, "outcome_uncertain", err.Error())
			}
			if errors.Is(err, workspace.ErrDocumentConflict) {
				return fault(request, "destination_exists", "A file or open document uses this name.")
			}
			return ok(wire(value), err)
		}, true
	case coreprotocol.DocumentOpen:
		var input coreprotocol.DocumentOpenRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := docs.Open(owner, input.Path)
			return ok(wire(value), err)
		}, true
	case coreprotocol.DocumentRead:
		var input coreprotocol.DocumentReadRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := docs.Snapshot(input.ID)
			return ok(wire(value), err)
		}, true
	case coreprotocol.DocumentSync:
		var input coreprotocol.DocumentSyncRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := docs.Sync(owner, input.ID, input.After)
			if err == nil && value.Msgsize() > corewire.MaxMessage/2 {
				value.Changes = nil
				value.Reset = true
			}
			return ok(wire(value), err)
		}, true
	case coreprotocol.DocumentClose:
		var input coreprotocol.DocumentReadRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			return ok(wire(coreprotocol.Empty{}), docs.CloseView(owner, input.ID))
		}, true
	case coreprotocol.DocumentEdit:
		var input coreprotocol.DocumentEditRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := docs.Apply(document.Change{DocumentID: input.ID, RequestID: request.ID, Owner: owner, BaseRevision: input.Revision, GroupID: input.Group, Edits: input.Edits})
			result := responseValue(request, wire(value))
			if err != nil {
				result.Error = &coreprotocol.Fault{Code: "resource_failed", Message: err.Error()}
			}
			return result
		}, true
	case coreprotocol.DocumentFormat, coreprotocol.DocumentUndo, coreprotocol.DocumentRedo, coreprotocol.DocumentSave, coreprotocol.DocumentReload:
		var input coreprotocol.DocumentRevisionRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		if input.Encoding != nil && (request.Method != coreprotocol.DocumentSave || !input.Encoding.Valid()) {
			return bad(errors.New("encoding applies only to a save with a supported file encoding"))
		}
		store, files := a.ws.LocalHistory, a.ws.FileOps
		return func(ctx context.Context) coreprotocol.Response {
			if request.Method == coreprotocol.DocumentReload {
				value, err := docs.Reload(ctx, owner, input.ID, request.ID, input.Revision)
				return ok(wire(value), err)
			}
			if request.Method == coreprotocol.DocumentFormat {
				value, err := docs.Format(ctx, owner, input.ID, request.ID, input.Revision)
				return ok(wire(value), err)
			}
			if request.Method == coreprotocol.DocumentSave {
				if input.Encoding != nil {
					value, err := docs.SaveWithEncoding(ctx, owner, input.ID, input.Revision, *input.Encoding, store, files)
					if errors.Is(err, workspace.ErrDocumentSaveUncertain) {
						return fault(request, "outcome_uncertain", err.Error())
					}
					return ok(wire(value), err)
				}
				value, err := docs.Save(ctx, owner, input.ID, input.Revision, input.Reason)
				return ok(wire(value), err)
			}
			if request.Method == coreprotocol.DocumentUndo {
				value, err := docs.Undo(owner, input.ID, request.ID, input.Revision)
				return ok(wire(value), err)
			}
			value, err := docs.Redo(owner, input.ID, request.ID, input.Revision)
			return ok(wire(value), err)
		}, true
	case coreprotocol.ProjectFormat:
		var input coreprotocol.ProjectFormatRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		return func(ctx context.Context) coreprotocol.Response {
			value, err := a.ws.FormatProject(ctx)
			return ok(wire(value), err)
		}, true
	case coreprotocol.TerminalList:
		return func(context.Context) coreprotocol.Response {
			return ok(wire(coreprotocol.TerminalInfos(manager.List())), nil)
		}, true
	case coreprotocol.TerminalCreate:
		var input workspace.TerminalCreateOpts
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := terminals.Create(input)
			return ok(wire(value), err)
		}, true
	case coreprotocol.TerminalRows:
		var input coreprotocol.TerminalRowsRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := terminals.Rows(input.ID, terminalvt.RowRequest{Start: input.Start, Count: input.Count, ViewportStart: input.ViewportStart, HistoryEpoch: input.HistoryEpoch, HistoryBase: input.HistoryBase})
			return ok(wire(value), err)
		}, true
	case coreprotocol.TerminalControl, coreprotocol.TerminalClose:
		var input coreprotocol.TerminalIDRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			if request.Method == coreprotocol.TerminalControl {
				value, err := terminals.Acquire(input.ID, controller)
				return ok(wire(value), err)
			}
			terminals.Close(input.ID)
			return ok(wire(coreprotocol.Empty{}), nil)
		}, true
	case coreprotocol.TerminalRelease:
		var input workspace.TerminalLease
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		if input.Owner != controller {
			return bad(workspace.ErrTerminalControl)
		}
		return func(context.Context) coreprotocol.Response {
			return ok(wire(coreprotocol.Empty{}), terminals.Release(input))
		}, true
	case coreprotocol.TerminalInput:
		var input coreprotocol.TerminalInputRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		if input.Lease.Owner != controller {
			return bad(workspace.ErrTerminalControl)
		}
		return func(context.Context) coreprotocol.Response {
			var err error
			switch input.Kind {
			case "key":
				err = terminals.Key(input.Lease, input.Key)
			case "mouse":
				err = terminals.Mouse(input.Lease, input.Mouse)
			case "paste":
				err = terminals.Paste(input.Lease, input.Data)
			case "rename":
				err = terminals.Rename(input.Lease, string(input.Data))
			case "clear":
				err = terminals.Clear(input.Lease)
			case "focus":
				err = terminals.Focus(input.Lease, input.Focused)
			default:
				err = fmt.Errorf("invalid terminal input kind")
			}
			return ok(wire(coreprotocol.Empty{}), err)
		}, true
	case coreprotocol.TerminalResize:
		var input coreprotocol.TerminalResizeRequest
		if err := decodeRequest(request, &input); err != nil {
			return bad(err)
		}
		if input.Lease.Owner != controller {
			return bad(workspace.ErrTerminalControl)
		}
		return func(context.Context) coreprotocol.Response {
			return ok(wire(coreprotocol.Empty{}), terminals.Resize(input.Lease, input.Cols, input.Rows))
		}, true
	}
	return nil, false
}

// terminalMethod selects the terminal lane; every other resource effect keeps
// the document lane's admission order.
func terminalMethod(method string) bool {
	switch method {
	case coreprotocol.TerminalList, coreprotocol.TerminalCreate, coreprotocol.TerminalRows, coreprotocol.TerminalControl,
		coreprotocol.TerminalRelease, coreprotocol.TerminalInput, coreprotocol.TerminalResize, coreprotocol.TerminalClose:
		return true
	}
	return false
}

// Apply resource publication on the actor, after the worker's effect completes.
// Silent PTY creation and explicit close emit no synchronous manager event, so
// RPC and semantic control invalidate inventory through this same owner.
func (a *actor) applyResource(request coreprotocol.Request, result coreprotocol.Response) {
	if result.Error != nil {
		return
	}
	var id string
	switch request.Method {
	case coreprotocol.TerminalCreate:
		var info workspace.TerminalInfo
		if decode(result.Payload, &info) == nil {
			id = info.ID
		}
	case coreprotocol.TerminalClose:
		var input coreprotocol.TerminalIDRequest
		if decodeRequest(request, &input) == nil {
			id = input.ID
		}
	}
	if id != "" {
		a.broadcast(coreprotocol.Change{Terminals: []string{id}})
	}
}

func resourceMutation(method string) bool {
	switch method {
	case coreprotocol.DocumentRead, coreprotocol.DocumentSync, coreprotocol.TerminalList, coreprotocol.TerminalRows:
		return false
	case coreprotocol.DocumentOpen, coreprotocol.DocumentCreateUntitled, coreprotocol.DocumentOpenUntitled, coreprotocol.DocumentSaveUntitled, coreprotocol.DocumentReviewUntitled,
		coreprotocol.DocumentClose, coreprotocol.DocumentFormat, coreprotocol.DocumentEdit, coreprotocol.DocumentUndo, coreprotocol.DocumentRedo, coreprotocol.DocumentSave, coreprotocol.DocumentReload, coreprotocol.ProjectFormat,
		coreprotocol.TerminalCreate, coreprotocol.TerminalControl, coreprotocol.TerminalRelease, coreprotocol.TerminalInput, coreprotocol.TerminalResize, coreprotocol.TerminalClose:
		return true
	}
	return false
}
