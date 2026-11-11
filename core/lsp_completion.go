package core

import (
	"context"

	"crude/coreprotocol"
	"crude/document"
)

func (a *actor) prepareCompletions(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	switch request.Method {
	case coreprotocol.LSPCompletionsMethod, coreprotocol.LSPCompletionInspectMethod, coreprotocol.LSPCompletionReleaseMethod:
	default:
		return nil, false
	}
	w := a.ws
	owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
	if request.Method == coreprotocol.LSPCompletionsMethod {
		var in coreprotocol.CompletionsRequest
		if err := decodeRequest(request, &in); err != nil {
			return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
		}
		return func(ctx context.Context) coreprotocol.Response {
			set, err := w.RequestLSPCompletions(ctx, owner, in.Source, in.ViewID, in.Sequence, in.Line, in.Col, in.Trigger)
			if err != nil {
				return fault(request, "completion_failed", err.Error())
			}
			return response(request, coreprotocol.CompletionReply{Set: set})
		}, true
	}
	if request.Method == coreprotocol.LSPCompletionInspectMethod {
		var in coreprotocol.CompletionInspectRequest
		if err := decodeRequest(request, &in); err != nil {
			return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
		}
		return func(ctx context.Context) coreprotocol.Response {
			detail, err := w.InspectLSPCompletion(ctx, owner, in.ID, in.Digest, in.ItemID, in.Resolve)
			if err != nil {
				return fault(request, "completion_inspect_failed", err.Error())
			}
			return response(request, coreprotocol.CompletionReply{Inspection: detail})
		}, true
	}
	var in coreprotocol.CompletionReleaseRequest
	if err := decodeRequest(request, &in); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	return func(ctx context.Context) coreprotocol.Response {
		if err := ctx.Err(); err != nil {
			return fault(request, "cancelled", err.Error())
		}
		if w.LiveDocuments == nil {
			return fault(request, "completion_unavailable", "canonical source authority is unavailable")
		}
		if err := w.LiveDocuments.ReleaseLSPCompletions(owner, in.ViewID, in.Sequence); err != nil {
			return fault(request, "completion_release_failed", err.Error())
		}
		return response(request, coreprotocol.CompletionReply{})
	}, true
}
