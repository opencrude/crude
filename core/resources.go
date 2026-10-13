package core

import (
	"context"
	"encoding/json"
	"fmt"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/document"
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
	ok := func(value any, err error) coreprotocol.Response {
		if err != nil {
			return fault(request, "resource_failed", err.Error())
		}
		return response(request, value)
	}
	switch request.Method {
	case coreprotocol.DocumentOpen:
		var input coreprotocol.DocumentOpenRequest
		if err := decode(request.Payload, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := docs.Open(owner, input.Path)
			return ok(value, err)
		}, true
	case coreprotocol.DocumentRead:
		var input coreprotocol.DocumentReadRequest
		if err := decode(request.Payload, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := docs.Snapshot(input.ID)
			return ok(value, err)
		}, true
	case coreprotocol.DocumentSync:
		var input coreprotocol.DocumentSyncRequest
		if err := decode(request.Payload, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := docs.Sync(owner, input.ID, input.After)
			if err == nil {
				encoded, encodeErr := json.Marshal(value)
				if encodeErr != nil {
					return fault(request, "encode_failed", encodeErr.Error())
				}
				if len(encoded) > corewire.MaxPayloadSize {
					value.Changes = nil
					value.Reset = true
				}
			}
			return ok(value, err)
		}, true
	case coreprotocol.DocumentClose:
		var input coreprotocol.DocumentReadRequest
		if err := decode(request.Payload, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response { return ok(struct{}{}, docs.CloseView(owner, input.ID)) }, true
	case coreprotocol.DocumentEdit:
		var input coreprotocol.DocumentEditRequest
		if err := decode(request.Payload, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := docs.Apply(document.Change{DocumentID: input.ID, RequestID: request.ID, Owner: owner, BaseRevision: input.Revision, GroupID: input.Group, Edits: input.Edits})
			return ok(value, err)
		}, true
	case coreprotocol.DocumentFormat, coreprotocol.DocumentUndo, coreprotocol.DocumentRedo, coreprotocol.DocumentSave:
		var input coreprotocol.DocumentRevisionRequest
		if err := decode(request.Payload, &input); err != nil {
			return bad(err)
		}
		return func(ctx context.Context) coreprotocol.Response {
			if request.Method == coreprotocol.DocumentFormat {
				value, err := docs.Format(ctx, owner, input.ID, request.ID, input.Revision)
				return ok(value, err)
			}
			if request.Method == coreprotocol.DocumentSave {
				value, err := docs.Save(ctx, owner, input.ID, input.Revision, input.Reason)
				return ok(value, err)
			}
			if request.Method == coreprotocol.DocumentUndo {
				value, err := docs.Undo(owner, input.ID, request.ID, input.Revision)
				return ok(value, err)
			}
			value, err := docs.Redo(owner, input.ID, request.ID, input.Revision)
			return ok(value, err)
		}, true
	case coreprotocol.TerminalList:
		return func(context.Context) coreprotocol.Response { return ok(manager.List(), nil) }, true
	case coreprotocol.TerminalCreate:
		var input workspace.TerminalCreateOpts
		if err := decode(request.Payload, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := terminals.Create(input)
			return ok(value, err)
		}, true
	case coreprotocol.TerminalRows:
		var input coreprotocol.TerminalRowsRequest
		if err := decode(request.Payload, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			value, err := terminals.Rows(input.ID, input.Start, input.Count)
			return ok(value, err)
		}, true
	case coreprotocol.TerminalControl, coreprotocol.TerminalClose:
		var input coreprotocol.TerminalIDRequest
		if err := decode(request.Payload, &input); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			if request.Method == coreprotocol.TerminalControl {
				value, err := terminals.Acquire(input.ID, controller)
				return ok(value, err)
			}
			terminals.Close(input.ID)
			return ok(struct{}{}, nil)
		}, true
	case coreprotocol.TerminalRelease:
		var input workspace.TerminalLease
		if err := decode(request.Payload, &input); err != nil {
			return bad(err)
		}
		if input.Owner != controller {
			return bad(workspace.ErrTerminalControl)
		}
		return func(context.Context) coreprotocol.Response { return ok(struct{}{}, terminals.Release(input)) }, true
	case coreprotocol.TerminalInput:
		var input coreprotocol.TerminalInputRequest
		if err := decode(request.Payload, &input); err != nil {
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
			case "focus":
				err = terminals.Focus(input.Lease, input.Focused)
			default:
				err = fmt.Errorf("invalid terminal input kind")
			}
			return ok(struct{}{}, err)
		}, true
	case coreprotocol.TerminalResize:
		var input coreprotocol.TerminalResizeRequest
		if err := decode(request.Payload, &input); err != nil {
			return bad(err)
		}
		if input.Lease.Owner != controller {
			return bad(workspace.ErrTerminalControl)
		}
		return func(context.Context) coreprotocol.Response {
			return ok(struct{}{}, terminals.Resize(input.Lease, input.Cols, input.Rows))
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
// Explicit PTY close intentionally emits no synchronous manager event, so both
// RPC and semantic control must invalidate inventory through this same owner.
func (a *actor) applyResource(request coreprotocol.Request, result coreprotocol.Response) {
	if result.Error != nil || request.Method != coreprotocol.TerminalClose {
		return
	}
	var input coreprotocol.TerminalIDRequest
	if decode(request.Payload, &input) == nil && input.ID != "" {
		a.broadcast(coreprotocol.Change{Terminals: []string{input.ID}})
	}
}

func resourceMutation(method string) bool {
	switch method {
	case coreprotocol.DocumentRead, coreprotocol.DocumentSync, coreprotocol.TerminalList, coreprotocol.TerminalRows:
		return false
	case coreprotocol.DocumentOpen, coreprotocol.DocumentClose, coreprotocol.DocumentFormat, coreprotocol.DocumentEdit, coreprotocol.DocumentUndo, coreprotocol.DocumentRedo, coreprotocol.DocumentSave,
		coreprotocol.TerminalCreate, coreprotocol.TerminalControl, coreprotocol.TerminalRelease, coreprotocol.TerminalInput, coreprotocol.TerminalResize, coreprotocol.TerminalClose:
		return true
	}
	return false
}
