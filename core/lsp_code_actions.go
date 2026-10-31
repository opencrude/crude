package core

import (
	"context"

	"crude/coreprotocol"
	"crude/document"
)

func (a *actor) prepareCodeActions(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	switch request.Method {
	case coreprotocol.LSPCodeActions, coreprotocol.LSPPrepareCodeAction, coreprotocol.LSPReleaseCodeActions:
	default:
		return nil, false
	}
	w := a.ws
	owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
	if request.Method == coreprotocol.LSPCodeActions {
		var in coreprotocol.CodeActionsRequest
		if err := decodeRequest(request, &in); err != nil {
			return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
		}
		return func(ctx context.Context) coreprotocol.Response {
			set, err := w.RequestLSPCodeActions(ctx, owner, in.Source, in.Start, in.End)
			if err != nil {
				return fault(request, "code_actions_failed", err.Error())
			}
			return response(request, coreprotocol.CodeActionsReply{Set: set})
		}, true
	}
	var in coreprotocol.CodeActionRequest
	if err := decodeRequest(request, &in); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	return func(ctx context.Context) coreprotocol.Response {
		if err := ctx.Err(); err != nil {
			return fault(request, "cancelled", err.Error())
		}
		if request.Method == coreprotocol.LSPPrepareCodeAction {
			preview, err := w.PrepareLSPCodeAction(ctx, owner, in.ID, in.Digest, in.Index)
			if err != nil {
				return fault(request, "code_actions_failed", err.Error())
			}
			return response(request, coreprotocol.CodeActionsReply{Preview: preview})
		}
		if w.LiveDocuments == nil {
			return fault(request, "code_actions_unavailable", "canonical source authority is unavailable")
		}
		if err := w.LiveDocuments.ReleaseLSPCodeActions(owner, in.ID, in.Digest); err != nil {
			return fault(request, "code_actions_failed", err.Error())
		}
		return response(request, coreprotocol.CodeActionsReply{})
	}, true
}
