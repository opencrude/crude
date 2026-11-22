package coreprotocol

import (
	"crude/types/insight"
)

// LSPSelectionFormattingMethod names the call that formats an editor selection.
const LSPSelectionFormattingMethod = "lsp.selection_formatting"

// SelectionFormattingRequest carries a selection formatting request.
type SelectionFormattingRequest struct{ Request insight.LSPFormattingRequest }

// SelectionFormattingReply carries the formatted selection.
type SelectionFormattingReply struct{ Result insight.LSPFormattingResult }
