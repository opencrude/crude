package types

// Standard document-highlight kinds. Text is a textual occurrence; Read and
// Write distinguish symbol access when the provider knows it.
const (
	LSPHighlightText  = 1
	LSPHighlightRead  = 2
	LSPHighlightWrite = 3

	LSPHighlightsFound       = "highlights"
	LSPHighlightsNull        = "null"
	LSPHighlightsEmpty       = "empty"
	LSPHighlightsUnsupported = "unsupported"
)

// LSPHighlightResponse is detached transport evidence for one position.
type LSPHighlightResponse struct {
	WireVersion int
	JSON        []byte
	Unsupported bool
}

// LSPHighlight is one occurrence in the invoking document, in zero-based
// UTF-16 coordinates checked against the exact source.
type LSPHighlight struct {
	Range LSPNavigationRange
	Kind  int
}

// LSPHighlights is one source-, provider- and caret-qualified answer. It is
// passive presentation evidence: never a selection, an edit or a rename.
type LSPHighlights struct {
	Boundary    InsightBoundary
	WireVersion int
	Line, Col   int
	Answer      string
	Highlights  []LSPHighlight
}
