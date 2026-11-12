package core

import (
	"context"
	"crude/coreprotocol"
	"crude/document"
	"crude/workspace"
	"errors"
)

func (a *actor) prepareSemantic(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	if request.Method != coreprotocol.LSPSemanticMethod && request.Method != coreprotocol.LSPSemanticReleaseMethod {
		return nil, false
	}
	owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
	w := a.ws
	if request.Method == coreprotocol.LSPSemanticMethod {
		var in coreprotocol.SemanticRequest
		if err := decodeRequest(request, &in); err != nil {
			return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
		}
		return func(ctx context.Context) coreprotocol.Response {
			set, err := w.RequestLSPSemantic(ctx, owner, in.Source, in.ViewID, in.Sequence, in.Range)
			if errors.Is(err, workspace.ErrSemanticBusy) {
				return fault(request, "busy", err.Error())
			}
			if err != nil {
				return fault(request, "semantic_tokens_failed", err.Error())
			}
			return response(request, coreprotocol.SemanticReply{Set: set})
		}, true
	}
	var in coreprotocol.SemanticReleaseRequest
	if err := decodeRequest(request, &in); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	return func(ctx context.Context) coreprotocol.Response {
		if err := ctx.Err(); err != nil {
			return fault(request, "cancelled", err.Error())
		}
		if w.LiveDocuments == nil {
			return fault(request, "semantic_tokens_unavailable", "shared source is unavailable")
		}
		if err := w.LiveDocuments.ReleaseLSPSemantic(owner, in.ViewID, in.Sequence); err != nil {
			return fault(request, "semantic_release_failed", err.Error())
		}
		return response(request, coreprotocol.SemanticReply{})
	}, true
}
