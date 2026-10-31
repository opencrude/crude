package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
)

var _ shellport.ReviewedEdits = (*Workspace)(nil)

func (w *Workspace) PrepareLSPRename(ctx context.Context, source types.InsightSource, line, col int, name string) (workspace.ReviewedEditPreview, error) {
	var out coreprotocol.ReviewedEditReply
	err := w.callCancellable(ctx, coreprotocol.LSPPrepareRename, &coreprotocol.RenamePreviewRequest{
		Source: source, Line: line, Col: col, NewName: name,
	}, &out)
	return out.Preview, err
}

func (w *Workspace) ApplyReviewedEdit(ctx context.Context, id, digest string) (workspace.ReviewedEditResult, error) {
	var out coreprotocol.ReviewedEditReply
	err := w.callCancellable(ctx, coreprotocol.ReviewedEditApply, &coreprotocol.ReviewedEditRequest{ID: id, Digest: digest}, &out)
	return out.Result, err
}

func (w *Workspace) ReleaseReviewedEdit(ctx context.Context, id, digest string) error {
	return w.callCancellable(ctx, coreprotocol.ReviewedEditRelease, &coreprotocol.ReviewedEditRequest{ID: id, Digest: digest}, &coreprotocol.ReviewedEditReply{})
}

func (w *Workspace) InspectReviewedEdit(ctx context.Context, id, digest string) (workspace.ReviewedEditResult, error) {
	var out coreprotocol.ReviewedEditReply
	err := w.callCancellable(ctx, coreprotocol.ReviewedEditInspect, &coreprotocol.ReviewedEditRequest{ID: id, Digest: digest}, &out)
	return out.Result, err
}
