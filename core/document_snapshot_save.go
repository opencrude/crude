package core

import (
	"context"
	"slices"

	"crude/coreprotocol"
)

func documentSnapshotSaveMutation(method string) bool {
	return method == coreprotocol.DocumentSnapshotSave
}

// prepareDocumentSnapshotSave captures the disk authority on the actor; only
// the returned worker performs I/O. The runtime joins workers before teardown.
func (a *actor) prepareDocumentSnapshotSave(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	if request.Method != coreprotocol.DocumentSnapshotSave {
		return nil, false
	}
	var input coreprotocol.DocumentSnapshotSaveRequest
	if err := decode(request.Payload, &input); err != nil {
		return func(context.Context) coreprotocol.Response {
			return fault(request, "invalid_request", err.Error())
		}, true
	}
	service := a.ws.Documents
	if service == nil {
		return func(context.Context) coreprotocol.Response {
			return fault(request, "document_service_missing", "document disk authority is unavailable")
		}, true
	}
	input.Request.Data = slices.Clone(input.Request.Data)
	return func(ctx context.Context) coreprotocol.Response {
		result := service.Save(ctx, input.Request)
		return response(request, coreprotocol.NewDocumentSnapshotSaveResult(result))
	}, true
}
