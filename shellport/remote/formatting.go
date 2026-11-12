package remote

import (
	"context"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
)

var _ shellport.SelectionFormatter = (*Workspace)(nil)

func (w *Workspace) RequestSelectionFormatting(ctx context.Context, in types.LSPFormattingRequest) (types.LSPFormattingResult, error) {
	var out coreprotocol.SelectionFormattingReply
	err := w.callCancellable(ctx, coreprotocol.LSPSelectionFormattingMethod, &coreprotocol.SelectionFormattingRequest{Request: in}, &out)
	return out.Result, err
}
