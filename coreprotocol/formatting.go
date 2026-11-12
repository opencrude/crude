package coreprotocol

import "crude/types"

const LSPSelectionFormattingMethod = "lsp.selection_formatting"

type SelectionFormattingRequest struct{ Request types.LSPFormattingRequest }
type SelectionFormattingReply struct{ Result types.LSPFormattingResult }
