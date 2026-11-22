package remote

import (
	"context"

	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/types/insight"
)

var _ shellport.Completions = (*Workspace)(nil)

// RequestLSPCompletions asks the language server for completions at a position.
func (w *Workspace) RequestLSPCompletions(ctx context.Context, source insight.Source, view string, sequence uint64, line, col int, trigger insight.LSPCompletionContext) (insight.LSPCompletionSet, error) {
	var out coreprotocol.CompletionReply
	err := w.callCancellable(ctx, coreprotocol.LSPCompletionsMethod, &coreprotocol.CompletionsRequest{
		Source: source, ViewID: view, Sequence: sequence, Line: line, Col: col, Trigger: trigger,
	}, &out)
	return out.Set, err
}

// InspectLSPCompletion returns the details of one completion item, resolved when asked.
func (w *Workspace) InspectLSPCompletion(ctx context.Context, id, digest, itemID string, resolve bool) (insight.LSPCompletionInspection, error) {
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
