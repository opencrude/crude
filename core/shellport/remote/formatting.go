package remote

import (
	"context"
	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/types/insight"
)

var _ shellport.SelectionFormatter = (*Workspace)(nil)

// RequestSelectionFormatting formats an editor selection through the language server.
func (w *Workspace) RequestSelectionFormatting(ctx context.Context, in insight.LSPFormattingRequest) (insight.LSPFormattingResult, error) {
	var out coreprotocol.SelectionFormattingReply
	err := w.callCancellable(ctx, coreprotocol.LSPSelectionFormattingMethod, &coreprotocol.SelectionFormattingRequest{Request: in}, &out)
	return out.Result, err
}
