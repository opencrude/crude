package remote

import (
	"context"

	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/types/insight"
)

var _ shellport.ProjectTools = (*Workspace)(nil)

// ReviewProjectTools previews the code tools that a project setup would add.
func (w *Workspace) ReviewProjectTools(ctx context.Context, request insight.ProjectToolSetupRequest) (insight.ProjectToolSetupReview, error) {
	var out coreprotocol.ProjectToolsReviewResult
	err := w.callCancellable(ctx, coreprotocol.ProjectToolsReview, &coreprotocol.ProjectToolsReviewRequest{Selection: request}, &out)
	return out.Preview, err
}

// DecideProjectTools applies the selected tools of a reviewed preview.
func (w *Workspace) DecideProjectTools(ctx context.Context, previewID string, selected []string) (insight.ProjectToolSetupResult, error) {
	var out coreprotocol.ProjectToolsDecideResult
	err := w.callCancellable(ctx, coreprotocol.ProjectToolsDecide, &coreprotocol.ProjectToolsDecideRequest{
		PreviewID: previewID, SelectedIDs: append([]string(nil), selected...),
	}, &out)
	return out.Result, err
}
