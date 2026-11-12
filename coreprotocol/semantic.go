package coreprotocol

import "crude/types"

const (
	LSPSemanticMethod        = "lsp.semantic_tokens"
	LSPSemanticReleaseMethod = "lsp.semantic_tokens_release"
)

type SemanticRequest struct {
	Source   types.InsightSource
	ViewID   string
	Sequence uint64
	Range    types.LSPSemanticRange
}
type SemanticReleaseRequest struct {
	ViewID   string
	Sequence uint64
}
type SemanticReply struct{ Set types.LSPSemanticSet }
