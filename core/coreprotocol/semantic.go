package coreprotocol

import (
	"crude/types/insight"
)

const (
	LSPSemanticMethod        = "lsp.semantic_tokens"
	LSPSemanticReleaseMethod = "lsp.semantic_tokens_release"
)

type SemanticRequest struct {
	Source   insight.Source
	ViewID   string
	Sequence uint64
	Range    insight.LSPSemanticRange
}
type SemanticReleaseRequest struct {
	ViewID   string
	Sequence uint64
}

// SemanticReply carries a semantic token set.
type SemanticReply struct{ Set insight.LSPSemanticSet }
