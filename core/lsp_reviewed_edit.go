package core

import (
	"context"

	"crude/coreprotocol"
	"crude/document"
)

func reviewedLSPMethod(method string) bool {
	switch method {
	case coreprotocol.LSPPrepareRename, coreprotocol.ReviewedEditApply,
		coreprotocol.ReviewedEditRelease, coreprotocol.ReviewedEditInspect:
		return true
	}
	return false
}

func (a *actor) prepareReviewedLSP(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	if !reviewedLSPMethod(request.Method) {
		return nil, false
	}
	w := a.ws
	docs, history, files := w.LiveDocuments, w.LocalHistory, w.FileOps
	owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
	bad := func(err error) (func(context.Context) coreprotocol.Response, bool) {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	if request.Method == coreprotocol.LSPPrepareRename {
		var in coreprotocol.RenamePreviewRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad(err)
		}
		return func(ctx context.Context) coreprotocol.Response {
			preview, err := w.PrepareLSPRename(ctx, owner, in.Source, in.Line, in.Col, in.NewName)
			if err != nil {
				return fault(request, "review_failed", err.Error())
			}
			return response(request, coreprotocol.ReviewedEditReply{Preview: preview})
		}, true
	}
	var in coreprotocol.ReviewedEditRequest
	if err := decodeRequest(request, &in); err != nil {
		return bad(err)
	}
	return func(ctx context.Context) coreprotocol.Response {
		if docs == nil {
			return fault(request, "review_unavailable", "canonical edit authority is unavailable")
		}
		if request.Method == coreprotocol.ReviewedEditApply {
			result := docs.ApplyReviewedEdit(ctx, owner, in.ID, in.Digest, history, files)
			// Domain refusal/uncertainty is a typed original result, not a
			// transport error which would discard the safety reference.
			return response(request, coreprotocol.ReviewedEditReply{Result: result})
		}
		if request.Method == coreprotocol.ReviewedEditRelease {
			if err := docs.ReleaseReviewedEdit(owner, in.ID, in.Digest); err != nil {
				return fault(request, "review_failed", err.Error())
			}
			return response(request, coreprotocol.ReviewedEditReply{})
		}
		result, err := docs.InspectReviewedEdit(ctx, owner, in.ID, in.Digest)
		if err != nil {
			return fault(request, "review_failed", err.Error())
		}
		return response(request, coreprotocol.ReviewedEditReply{Result: result})
	}, true
}
