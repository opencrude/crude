package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
)

var _ shellport.ProjectTools = (*Workspace)(nil)

func (w *Workspace) ReviewProjectTools(ctx context.Context, explicit bool) (types.ProjectToolSetupReview, error) {
	var out coreprotocol.ProjectToolsReviewResult
	err := w.callCancellable(ctx, coreprotocol.ProjectToolsReview, &coreprotocol.ProjectToolsReviewRequest{Explicit: explicit}, &out)
	return out.Preview, err
}

func (w *Workspace) DecideProjectTools(ctx context.Context, previewID string, selected []string) (types.ProjectToolSetupResult, error) {
	var out coreprotocol.ProjectToolsDecideResult
	err := w.callCancellable(ctx, coreprotocol.ProjectToolsDecide, &coreprotocol.ProjectToolsDecideRequest{
		PreviewID: previewID, SelectedIDs: append([]string(nil), selected...),
	}, &out)
	return out.Result, err
}
