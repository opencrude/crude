package coreprotocol

import (
	"crude/types/insight"
)

const LSPHighlightsMethod = "lsp.highlights"

// HighlightsRequest asks for the occurrences of the symbol at one caret of an
// acknowledged canonical source. The reply is passive presentation evidence.
type HighlightsRequest struct {
	Source    insight.Source
	Line, Col int
}

type HighlightsReply struct {
	Result insight.LSPHighlights
}
