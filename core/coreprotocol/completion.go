package coreprotocol

import (
	"crude/types/insight"
)

const (
	LSPCompletionsMethod       = "lsp.completions"
	LSPCompletionInspectMethod = "lsp.completion_inspect"
	LSPCompletionReleaseMethod = "lsp.completion_release"
)

type CompletionsRequest struct {
	Source    insight.Source
	ViewID    string
	Sequence  uint64
	Line, Col int
	Trigger   insight.LSPCompletionContext
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
	Set        insight.LSPCompletionSet
	Inspection insight.LSPCompletionInspection
}
