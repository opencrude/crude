package core

import (
	"context"

	"crude/coreprotocol"
	"crude/document"
)

// prepareProjectTools uses the same authenticated native-shell boundary as
// history protection. No semantic-domain command exposes the decision method.
func (a *actor) prepareProjectTools(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	if request.Method != coreprotocol.ProjectToolsReview && request.Method != coreprotocol.ProjectToolsDecide {
		return nil, false
	}
	owner := document.Owner{ClientID: request.Owner.ClientID, Epoch: request.Owner.Epoch}
	service := a.projectTools
	var review coreprotocol.ProjectToolsReviewRequest
	var decide coreprotocol.ProjectToolsDecideRequest
	var input coreprotocol.Target = &review
	if request.Method == coreprotocol.ProjectToolsDecide {
		input = &decide
	}
	if request.JSON || owner.ClientID == "" || owner.Epoch == 0 {
		return func(context.Context) coreprotocol.Response {
			return fault(request, "human_only", "Review and confirm project tools in Crude.")
		}, true
	}
	if err := decodeRequest(request, input); err != nil {
		return func(context.Context) coreprotocol.Response {
			return fault(request, "invalid_request", err.Error())
		}, true
	}
	return func(ctx context.Context) coreprotocol.Response {
		if service == nil {
			return fault(request, "setup_unavailable", "Code intelligence setup is unavailable. Reopen the folder and try again.")
		}
		if request.Method == coreprotocol.ProjectToolsReview {
			preview, err := service.Review(ctx, owner, review.Explicit)
			out := response(request, coreprotocol.ProjectToolsReviewResult{Preview: preview})
			if err != nil {
				out.Error = &coreprotocol.Fault{Code: "setup_failed", Message: err.Error()}
			}
			return out
		}
		result, err := service.Decide(ctx, owner, request.ID, decide.PreviewID, decide.SelectedIDs)
		out := response(request, coreprotocol.ProjectToolsDecideResult{Result: result})
		if err != nil {
			out.Error = &coreprotocol.Fault{Code: "setup_failed", Message: err.Error()}
		}
		return out
	}, true
}
