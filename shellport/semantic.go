package shellport

import (
	"context"
	"crude/types"
)

// SemanticTokens is passive, source-qualified evidence. The core owns the
// result ID and delta base; shells supply only their exact view sequence.
type SemanticTokens interface {
	RequestLSPSemantic(context.Context, types.InsightSource, string, uint64, types.LSPSemanticRange) (types.LSPSemanticSet, error)
	ReleaseLSPSemantic(context.Context, string, uint64) error
}
