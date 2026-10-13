package core

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"crude/coreprotocol"
	"crude/types/control"
	"crude/workspace"
)

// prepareControlDomain is called on the actor AFTER the gateway has resolved
// and authorized the actual descriptor. owner is trusted gateway attribution;
// it is never derived from request input or a claimed shell ID.
func (a *actor) prepareControlDomain(in control.Request, owner coreprotocol.Owner) (func(context.Context) control.Result, func(control.Result) control.Result, bool) {
	if run, apply, handled := a.prepareControlHistory(in, owner); handled {
		return run, apply, true
	}
	if strings.HasPrefix(in.CommandID, "execution.") {
		return a.prepareControlExecution(in, owner)
	}
	if in.CommandID == "settings.fields" {
		return a.prepareControlSettingsFields(in)
	}
	if in.CommandID == "settings.get" || in.CommandID == "settings.set" || in.CommandID == "settings.reset" {
		return a.prepareControlSettings(in, owner)
	}
	methods := map[string]string{
		"session.list":  coreprotocol.ListSessions,
		"document.open": coreprotocol.DocumentOpen, "document.read": coreprotocol.DocumentRead,
		"document.edit": coreprotocol.DocumentEdit, "document.save": coreprotocol.DocumentSave,
		"document.undo": coreprotocol.DocumentUndo, "document.redo": coreprotocol.DocumentRedo, "document.format": coreprotocol.DocumentFormat,
		"terminal.list": coreprotocol.TerminalList, "terminal.create": coreprotocol.TerminalCreate,
		"terminal.rows": coreprotocol.TerminalRows, "terminal.input": coreprotocol.TerminalInput,
		"terminal.control": coreprotocol.TerminalControl, "terminal.close": coreprotocol.TerminalClose,
		"files.list": coreprotocol.FilesBrowse, "files.read": coreprotocol.FilesRead,
		"files.search": coreprotocol.FilesSearch, "files.create": coreprotocol.FilesCreate,
		"files.rename": coreprotocol.FilesRename, "files.delete": coreprotocol.FilesDelete,
		"git.snapshot": coreprotocol.GitSnapshot, "git.review": coreprotocol.GitCompare,
		"git.stage_reviewed": coreprotocol.GitStageReviewed, "git.discard_preview": coreprotocol.PreviewGitDiscard,
		"git.discard_reviewed": coreprotocol.DiscardGitReviewed,
		"history.timeline":     coreprotocol.HistoryTimeline, "history.plan": coreprotocol.HistoryPlan, "history.apply": coreprotocol.HistoryApply,
	}
	method, known := methods[in.CommandID]
	if !known {
		return nil, nil, false
	} // session lifecycle is gateway-owned.
	request := coreprotocol.Request{ID: in.ID, Owner: owner, WorkspaceID: in.Target.WorkspaceID, Method: method, Payload: in.Input}
	if owner.ClientID == "" || owner.Epoch == 0 {
		return controlFailure(in, control.Invalid, "trusted caller owner is required")
	}
	// Target resource and payload identity must agree; neither is a fallback for
	// the other. Revisions stay in their typed owner requests.
	var identity struct {
		ID       string  `json:"id"`
		Revision *uint64 `json:"revision"`
	}
	_ = json.Unmarshal(in.Input, &identity)
	if in.Target.ResourceID != "" && identity.ID != in.Target.ResourceID {
		return controlFailure(in, control.Invalid, "resource target differs from input identity")
	}
	if in.Preconditions.Revision != nil && (identity.Revision == nil || *identity.Revision != *in.Preconditions.Revision) {
		return controlFailure(in, control.Stale, "revision precondition differs from typed input")
	}
	root, err := a.controlDomainRoot(context.Background(), in)
	if err != nil {
		return controlFailure(in, control.Denied, err.Error())
	}
	request, paths, err := a.controlDomainPaths(request, root)
	if err != nil {
		return controlFailure(in, control.Denied, err.Error())
	}
	if in.CommandID == "session.list" {
		// Capture only the immutable listing owner, not live session maps.
		root, db, logger := a.ws.RootPath, a.ws.WorkspaceDB, a.ws.Logger
		return wrapControlOperation(in, func(context.Context) coreprotocol.Response {
			reader := &workspace.Workspace{RootPath: root, WorkspaceDB: db, Logger: logger}
			return response(request, reader.ListSessions())
		}, nil)
	}
	var run func(context.Context) coreprotocol.Response
	var handled bool
	var apply func(coreprotocol.Response) error
	if run, handled = a.prepareResource(request); handled {
		apply = func(result coreprotocol.Response) error { a.applyResource(request, result); return nil }
	} else {
		if run, handled = a.prepareFilesAtRoot(request, root); !handled {
			run, handled = a.prepareServiceAtRoot(request, root)
			apply = func(result coreprotocol.Response) error { a.applyService(request, result); return nil }
		} else {
			apply = func(result coreprotocol.Response) error { return a.applyFiles(request, result) }
		}
	}
	if !handled {
		return nil, nil, false
	}
	operation := run
	run = func(ctx context.Context) coreprotocol.Response {
		actual, err := a.controlDomainRoot(ctx, in)
		if err != nil || actual != root {
			return fault(request, "ownership_blocked", "execution authority changed before effect")
		}
		// Re-resolve document identity immediately before the effect; relocation
		// may have changed its path since actor preparation.
		_, currentPaths, err := a.controlDomainPaths(request, root)
		if err != nil {
			return fault(request, "ownership_blocked", err.Error())
		}
		if len(currentPaths) != 0 {
			paths = currentPaths
		}
		release, err := a.controlDomainClaims(ctx, in, paths)
		if err != nil {
			return fault(request, "ownership_blocked", err.Error())
		}
		defer release()
		return operation(ctx)
	}
	return wrapControlOperation(in, run, apply)
}

func controlFailure(in control.Request, code control.FaultCode, message string) (func(context.Context) control.Result, func(control.Result) control.Result, bool) {
	return func(context.Context) control.Result {
		return control.Result{RequestID: in.ID, OperationID: in.ID, State: control.Failed, Fault: &control.Fault{Code: code, Message: message}}
	}, nil, true
}

// Retain the exact typed receipt privately. The gateway calls apply on the
// actor; no caller-supplied result can manufacture a settings publication.
func wrapControlOperation(in control.Request, run func(context.Context) coreprotocol.Response, apply func(coreprotocol.Response) error) (func(context.Context) control.Result, func(control.Result) control.Result, bool) {
	var mu sync.Mutex
	var once sync.Once
	var receipt coreprotocol.Response
	work := func(ctx context.Context) control.Result {
		mu.Lock()
		defer mu.Unlock()
		once.Do(func() {
			if ctx.Err() != nil {
				receipt = coreprotocol.Response{Error: &coreprotocol.Fault{Code: "cancelled", Message: "control operation cancelled"}}
				return
			}
			receipt = run(ctx)
			// The file RPC contract returns a typed failed operation as data. The
			// semantic control result must not mistake that domain failure for a
			// committed effect, while ordinary RPC consumers keep their contract.
			if strings.HasPrefix(in.CommandID, "files.") && receipt.Error == nil {
				var operation coreprotocol.FileOperationResult
				if json.Unmarshal(receipt.Payload, &operation) == nil && operation.Error != "" {
					receipt.Error = &coreprotocol.Fault{Code: "operation_failed", Message: operation.Error}
				}
			}
		})
		out := control.Result{RequestID: in.ID, OperationID: in.ID, State: control.Committed, Output: append(json.RawMessage(nil), receipt.Payload...)}
		if in.Target.ResourceID != "" {
			out.ResourceIDs = []string{in.Target.ResourceID}
		}
		var identity struct {
			ID          string `json:"id"`
			OperationID string `json:"operation_id"`
		}
		if json.Unmarshal(receipt.Payload, &identity) == nil {
			for _, id := range []string{identity.ID, identity.OperationID} {
				if id != "" && (len(out.ResourceIDs) == 0 || out.ResourceIDs[0] != id) {
					out.ResourceIDs = append(out.ResourceIDs, id)
				}
			}
		}
		if receipt.Error != nil {
			code := control.OperationFailed
			switch receipt.Error.Code {
			case "ownership_blocked":
				code = control.Denied
			case "invalid_request":
				code = control.Invalid
			case "stale_preimage":
				code = control.Stale
			case "session_missing", "settings_unavailable", "unavailable":
				code = control.Unavailable
			}
			out.State, out.Output, out.Fault = control.Failed, nil, &control.Fault{Code: code, Message: receipt.Error.Message}
		}
		return out
	}
	var publish func(control.Result) control.Result
	if apply != nil {
		var applied sync.Once
		var publicationErr error
		publish = func(result control.Result) control.Result {
			mu.Lock()
			defer mu.Unlock()
			if result.RequestID == in.ID && result.OperationID == in.ID && result.State == control.Committed && receipt.Error == nil && receipt.ID == in.ID {
				applied.Do(func() { publicationErr = apply(receipt) })
			}
			if publicationErr != nil {
				result.State = control.Partial
				result.Fault = &control.Fault{Code: control.OperationFailed, Message: "effect committed but publication failed: " + publicationErr.Error()}
			}
			return result
		}
	}
	return work, publish, true
}
