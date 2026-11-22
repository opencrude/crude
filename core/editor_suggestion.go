package core

import (
	"context"

	"crude/core/coreprotocol"
	"crude/workspace/document"
)

// prepareSuggestions serves editor suggestions on resource workers. Requests
// never run on the actor: they read the ledger and call the model.
func (a *actor) prepareSuggestions(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	switch request.Method {
	case coreprotocol.EditorSuggestMethod, coreprotocol.EditorSuggestReleaseMethod,
		coreprotocol.EditorSuggestStatusMethod, coreprotocol.EditorRecentEditsMethod:
	default:
		return nil, false
	}
	w := a.ws
	owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
	bad := func(err error) (func(context.Context) coreprotocol.Response, bool) {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	switch request.Method {
	case coreprotocol.EditorSuggestMethod:
		var in coreprotocol.EditorSuggestRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		return func(ctx context.Context) coreprotocol.Response {
			result, err := w.RequestEditorSuggestion(ctx, owner, in.Request)
			if err != nil {
				return fault(request, "suggestion_failed", err.Error())
			}
			return response(request, coreprotocol.EditorSuggestReply{Result: result, Status: result.Status})
		}, true
	case coreprotocol.EditorSuggestReleaseMethod:
		var in coreprotocol.EditorSuggestReleaseRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			w.ReleaseEditorSuggestion(owner, in.ViewID, in.Sequence)
			return response(request, coreprotocol.EditorSuggestReply{})
		}, true
	case coreprotocol.EditorSuggestStatusMethod:
		return func(ctx context.Context) coreprotocol.Response {
			return response(request, coreprotocol.EditorSuggestReply{Status: w.EditorSuggestionStatus(ctx)})
		}, true
	default:
		var in coreprotocol.EditorRecentEditsRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		return func(context.Context) coreprotocol.Response {
			limit := min(max(in.Limit, 1), 64)
			return response(request, coreprotocol.EditorSuggestReply{Edits: w.RecentEdits(owner, in.Path, limit)})
		}, true
	}
}
