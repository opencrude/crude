package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
)

var _ shellport.CodeActions = (*Workspace)(nil)

func (w *Workspace) RequestLSPCodeActions(ctx context.Context, source types.InsightSource, start, end int) (workspace.CodeActionSet, error) {
	var out coreprotocol.CodeActionsReply
	err := w.callCancellable(ctx, coreprotocol.LSPCodeActions, &coreprotocol.CodeActionsRequest{
		Source: source, Start: start, End: end,
	}, &out)
	return out.Set, err
}

func (w *Workspace) PrepareLSPCodeAction(ctx context.Context, id, digest string, index int) (workspace.ReviewedEditPreview, error) {
	var out coreprotocol.CodeActionsReply
	err := w.callCancellable(ctx, coreprotocol.LSPPrepareCodeAction, &coreprotocol.CodeActionRequest{
		ID: id, Digest: digest, Index: index,
	}, &out)
	return out.Preview, err
}

func (w *Workspace) ReleaseLSPCodeActions(ctx context.Context, id, digest string) error {
	var out coreprotocol.CodeActionsReply
	return w.callCancellable(ctx, coreprotocol.LSPReleaseCodeActions, &coreprotocol.CodeActionRequest{
		ID: id, Digest: digest,
	}, &out)
}
