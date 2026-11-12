package core

import (
	"context"
	"crude/coreprotocol"
	"crude/document"
)

func (a *actor) prepareSelectionFormatting(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	if request.Method != coreprotocol.LSPSelectionFormattingMethod {
		return nil, false
	}
	var in coreprotocol.SelectionFormattingRequest
	if err := decodeRequest(request, &in); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
	w := a.ws
	return func(ctx context.Context) coreprotocol.Response {
		out, err := w.RequestSelectionFormatting(ctx, owner, in.Request)
		if err != nil {
			return fault(request, "formatting_failed", err.Error())
		}
		return response(request, coreprotocol.SelectionFormattingReply{Result: out})
	}, true
}
