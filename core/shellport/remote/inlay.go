package remote

import (
	"context"

	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/types/insight"
)

var _ shellport.InlayHints = (*Workspace)(nil)

// RequestLSPInlayHints asks for the inlay hints in a range of a view.
func (w *Workspace) RequestLSPInlayHints(ctx context.Context, source insight.Source, view string, sequence uint64, span insight.LSPInlayRange) (insight.LSPInlaySet, error) {
	var out coreprotocol.InlayReply
	err := w.callCancellable(ctx, coreprotocol.LSPInlayHintsMethod, &coreprotocol.InlayHintsRequest{
		Source: source, ViewID: view, Sequence: sequence, Range: span,
	}, &out)
	return out.Set, err
}

// InspectLSPInlayHint returns the details of one inlay hint.
func (w *Workspace) InspectLSPInlayHint(ctx context.Context, id, digest string, index int, resolve bool) (insight.LSPInlayInspection, error) {
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
