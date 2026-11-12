package coreprotocol

import "crude/types"

const LSPHighlightsMethod = "lsp.highlights"

// HighlightsRequest asks for the occurrences of the symbol at one caret of an
// acknowledged canonical source. The reply is passive presentation evidence.
type HighlightsRequest struct {
	Source    types.InsightSource
	Line, Col int
}

type HighlightsReply struct {
	Result types.LSPHighlights
}
