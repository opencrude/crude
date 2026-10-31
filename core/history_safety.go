package core

import (
	"context"

	"crude/coreprotocol"
	"crude/document"
	"crude/workspace"
)

func (a *actor) prepareHistorySafety(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	switch request.Method {
	case coreprotocol.HistorySafetyList, coreprotocol.HistorySafetyInspect, coreprotocol.HistorySafetyChange:
	default:
		return nil, false
	}
	documents, store := a.ws.LiveDocuments, a.ws.LocalHistory
	owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
	var list coreprotocol.HistorySafetyListRequest
	var inspect coreprotocol.HistorySafetyInspectRequest
	var change coreprotocol.HistorySafetyChangeRequest
	var target coreprotocol.Target
	switch request.Method {
	case coreprotocol.HistorySafetyList:
		target = &list
	case coreprotocol.HistorySafetyInspect:
		target = &inspect
	case coreprotocol.HistorySafetyChange:
		target = &change
	}
	if err := decodeRequest(request, target); err != nil {
		return func(context.Context) coreprotocol.Response {
			return fault(request, "invalid_request", err.Error())
		}, true
	}
	return func(ctx context.Context) coreprotocol.Response {
		if documents == nil || store == nil {
			return fault(request, "history_unavailable", "original document and history authorities are unavailable")
		}
		var out coreprotocol.HistorySafetyReply
		var err error
		switch request.Method {
		case coreprotocol.HistorySafetyList:
			out.Page, err = documents.ListHistorySafety(ctx, store, workspace.HistorySafetyListRequest{
				Cursor: list.Cursor, Limit: list.Limit,
			})
		case coreprotocol.HistorySafetyInspect:
			out.Detail, err = documents.InspectHistorySafety(ctx, store, inspect.Reference)
		case coreprotocol.HistorySafetyChange:
			// Native attached-shell consent only. There is deliberately no
			// semantic-domain or MCP command that grants this human action.
			out.Result = documents.ChangeHistorySafetyProtection(ctx, store, owner, request.ID, change.Consent)
		}
		if err != nil {
			return fault(request, "history_safety_failed", err.Error())
		}
		return response(request, out)
	}, true
}
