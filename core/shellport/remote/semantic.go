package remote

import (
	"context"
	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/types/insight"
)

var _ shellport.SemanticTokens = (*Workspace)(nil)

// RequestLSPSemantic asks for the semantic tokens of a range in a view.
func (w *Workspace) RequestLSPSemantic(ctx context.Context, source insight.Source, view string, seq uint64, span insight.LSPSemanticRange) (insight.LSPSemanticSet, error) {
	var out coreprotocol.SemanticReply
	err := w.callCancellable(ctx, coreprotocol.LSPSemanticMethod, &coreprotocol.SemanticRequest{Source: source, ViewID: view, Sequence: seq, Range: span}, &out)
	return out.Set, err
}
func (w *Workspace) ReleaseLSPSemantic(ctx context.Context, view string, seq uint64) error {
	var out coreprotocol.SemanticReply
	return w.callCancellable(ctx, coreprotocol.LSPSemanticReleaseMethod, &coreprotocol.SemanticReleaseRequest{ViewID: view, Sequence: seq}, &out)
}
