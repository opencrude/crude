package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
)

var _ shellport.Completions = (*Workspace)(nil)

func (w *Workspace) RequestLSPCompletions(ctx context.Context, source types.InsightSource, view string, sequence uint64, line, col int, trigger types.LSPCompletionContext) (types.LSPCompletionSet, error) {
	var out coreprotocol.CompletionReply
	err := w.callCancellable(ctx, coreprotocol.LSPCompletionsMethod, &coreprotocol.CompletionsRequest{
		Source: source, ViewID: view, Sequence: sequence, Line: line, Col: col, Trigger: trigger,
	}, &out)
	return out.Set, err
}

func (w *Workspace) InspectLSPCompletion(ctx context.Context, id, digest, itemID string, resolve bool) (types.LSPCompletionInspection, error) {
	var out coreprotocol.CompletionReply
	err := w.callCancellable(ctx, coreprotocol.LSPCompletionInspectMethod, &coreprotocol.CompletionInspectRequest{
		ID: id, Digest: digest, ItemID: itemID, Resolve: resolve,
	}, &out)
	return out.Inspection, err
}

func (w *Workspace) ReleaseLSPCompletions(ctx context.Context, view string, sequence uint64) error {
	var out coreprotocol.CompletionReply
	return w.callCancellable(ctx, coreprotocol.LSPCompletionReleaseMethod, &coreprotocol.CompletionReleaseRequest{
		ViewID: view, Sequence: sequence,
	}, &out)
}
