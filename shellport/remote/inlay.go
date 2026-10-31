package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
)

var _ shellport.InlayHints = (*Workspace)(nil)

func (w *Workspace) RequestLSPInlayHints(ctx context.Context, source types.InsightSource, view string, sequence uint64, span types.LSPInlayRange) (types.LSPInlaySet, error) {
	var out coreprotocol.InlayReply
	err := w.callCancellable(ctx, coreprotocol.LSPInlayHintsMethod, &coreprotocol.InlayHintsRequest{
		Source: source, ViewID: view, Sequence: sequence, Range: span,
	}, &out)
	return out.Set, err
}

func (w *Workspace) InspectLSPInlayHint(ctx context.Context, id, digest string, index int, resolve bool) (types.LSPInlayInspection, error) {
	var out coreprotocol.InlayReply
	err := w.callCancellable(ctx, coreprotocol.LSPInlayInspectMethod, &coreprotocol.InlayInspectRequest{
		ID: id, Digest: digest, Index: index, Resolve: resolve,
	}, &out)
	return out.Inspection, err
}

func (w *Workspace) ReleaseLSPInlayHints(ctx context.Context, view string, sequence uint64) error {
	var out coreprotocol.InlayReply
	return w.callCancellable(ctx, coreprotocol.LSPInlayReleaseMethod, &coreprotocol.InlayReleaseRequest{
		ViewID: view, Sequence: sequence,
	}, &out)
}
