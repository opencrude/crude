package core

import (
	"context"

	"crude/coreprotocol"
	"crude/document"
)

func (a *actor) prepareInlay(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	switch request.Method {
	case coreprotocol.LSPInlayHintsMethod, coreprotocol.LSPInlayInspectMethod, coreprotocol.LSPInlayReleaseMethod:
	default:
		return nil, false
	}
	w := a.ws
	owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
	if request.Method == coreprotocol.LSPInlayHintsMethod {
		var in coreprotocol.InlayHintsRequest
		if err := decodeRequest(request, &in); err != nil {
			return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
		}
		return func(ctx context.Context) coreprotocol.Response {
			set, err := w.RequestLSPInlayHints(ctx, owner, in.Source, in.ViewID, in.Sequence, in.Range)
			if err != nil {
				return fault(request, "inlay_hints_failed", err.Error())
			}
			return response(request, coreprotocol.InlayReply{Set: set})
		}, true
	}
	if request.Method == coreprotocol.LSPInlayInspectMethod {
		var in coreprotocol.InlayInspectRequest
		if err := decodeRequest(request, &in); err != nil {
			return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
		}
		return func(ctx context.Context) coreprotocol.Response {
			detail, err := w.InspectLSPInlayHint(ctx, owner, in.ID, in.Digest, in.Index, in.Resolve)
			if err != nil {
				return fault(request, "inlay_inspect_failed", err.Error())
			}
			return response(request, coreprotocol.InlayReply{Inspection: detail})
		}, true
	}
	var in coreprotocol.InlayReleaseRequest
	if err := decodeRequest(request, &in); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	return func(ctx context.Context) coreprotocol.Response {
		if err := ctx.Err(); err != nil {
			return fault(request, "cancelled", err.Error())
		}
		if w.LiveDocuments == nil {
			return fault(request, "inlay_unavailable", "canonical source authority is unavailable")
		}
		if err := w.LiveDocuments.ReleaseLSPInlayHints(owner, in.ViewID, in.Sequence); err != nil {
			return fault(request, "inlay_release_failed", err.Error())
		}
		return response(request, coreprotocol.InlayReply{})
	}, true
}
