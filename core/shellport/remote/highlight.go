package remote

import (
	"context"

	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/types/insight"
)

var _ shellport.Highlights = (*Workspace)(nil)

// RequestLSPHighlights asks for the occurrences that relate to a position.
func (w *Workspace) RequestLSPHighlights(ctx context.Context, source insight.Source, line, col int) (insight.LSPHighlights, error) {
	var out coreprotocol.HighlightsReply
	err := w.callCancellable(ctx, coreprotocol.LSPHighlightsMethod, &coreprotocol.HighlightsRequest{Source: source, Line: line, Col: col}, &out)
	return out.Result, err
}
