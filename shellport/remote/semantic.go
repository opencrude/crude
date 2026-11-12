package remote

import (
	"context"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
)

var _ shellport.SemanticTokens = (*Workspace)(nil)

func (w *Workspace) RequestLSPSemantic(ctx context.Context, source types.InsightSource, view string, seq uint64, span types.LSPSemanticRange) (types.LSPSemanticSet, error) {
	var out coreprotocol.SemanticReply
	err := w.callCancellable(ctx, coreprotocol.LSPSemanticMethod, &coreprotocol.SemanticRequest{Source: source, ViewID: view, Sequence: seq, Range: span}, &out)
	return out.Set, err
}
func (w *Workspace) ReleaseLSPSemantic(ctx context.Context, view string, seq uint64) error {
	var out coreprotocol.SemanticReply
	return w.callCancellable(ctx, coreprotocol.LSPSemanticReleaseMethod, &coreprotocol.SemanticReleaseRequest{ViewID: view, Sequence: seq}, &out)
}
