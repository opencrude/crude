package core

import (
	"context"

	"crude/coreprotocol"
	"crude/document"
)

func (a *actor) prepareNavigation(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	switch request.Method {
	case coreprotocol.LSPNavigationMethod:
		var in coreprotocol.NavigationRequest
		if err := decodeRequest(request, &in); err != nil {
			return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
		}
		owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
		w := a.ws
		return func(ctx context.Context) coreprotocol.Response {
			if err := ctx.Err(); err != nil {
				return fault(request, "cancelled", err.Error())
			}
			result, err := w.RequestLSPNavigation(ctx, owner, in.Source, in.Kind, in.Line, in.Col, in.IncludeDeclaration)
			if err != nil {
				return fault(request, "navigation_failed", err.Error())
			}
			return response(request, coreprotocol.NavigationReply{Result: result})
		}, true
	case coreprotocol.LSPNavigationTargetMethod:
		var in coreprotocol.NavigationTargetRequest
		if err := decodeRequest(request, &in); err != nil {
			return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
		}
		owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
		w := a.ws
		return func(ctx context.Context) coreprotocol.Response {
			if err := ctx.Err(); err != nil {
				return fault(request, "cancelled", err.Error())
			}
			landing, err := w.InspectLSPNavigationTarget(ctx, owner, in.Boundary, in.WireVersion, in.Kind, in.Target)
			if err != nil {
				return fault(request, "navigation_target_failed", err.Error())
			}
			return response(request, coreprotocol.NavigationReply{Landing: landing})
		}, true
	default:
		return nil, false
	}
}
