package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
)

var _ shellport.Highlights = (*Workspace)(nil)

func (w *Workspace) RequestLSPHighlights(ctx context.Context, source types.InsightSource, line, col int) (types.LSPHighlights, error) {
	var out coreprotocol.HighlightsReply
	err := w.callCancellable(ctx, coreprotocol.LSPHighlightsMethod, &coreprotocol.HighlightsRequest{Source: source, Line: line, Col: col}, &out)
	return out.Result, err
}
