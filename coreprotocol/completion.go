package coreprotocol

import "crude/types"

const (
	LSPCompletionsMethod       = "lsp.completions"
	LSPCompletionInspectMethod = "lsp.completion_inspect"
	LSPCompletionReleaseMethod = "lsp.completion_release"
)

type CompletionsRequest struct {
	Source    types.InsightSource
	ViewID    string
	Sequence  uint64
	Line, Col int
}

// Only exact retained identifiers cross back to the canonical owner. Shells
// cannot author provider objects, resolve payloads or replacement insertions.
type CompletionInspectRequest struct {
	ID, Digest, ItemID string
	Resolve            bool
}

type CompletionReleaseRequest struct {
	ViewID   string
	Sequence uint64
}

type CompletionReply struct {
	Set        types.LSPCompletionSet
	Inspection types.LSPCompletionInspection
}
