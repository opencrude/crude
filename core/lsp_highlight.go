package core

import (
	"context"

	"crude/coreprotocol"
	"crude/document"
)

func (a *actor) prepareHighlights(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	if request.Method != coreprotocol.LSPHighlightsMethod {
		return nil, false
	}
	var in coreprotocol.HighlightsRequest
	if err := decodeRequest(request, &in); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
	w := a.ws
	return func(ctx context.Context) coreprotocol.Response {
		if err := ctx.Err(); err != nil {
			return fault(request, "cancelled", err.Error())
		}
		result, err := w.RequestLSPHighlights(ctx, owner, in.Source, in.Line, in.Col)
		if err != nil {
			return fault(request, "highlights_failed", err.Error())
		}
		return response(request, coreprotocol.HighlightsReply{Result: result})
	}, true
}
