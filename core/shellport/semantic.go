package shellport

import (
	"context"
	"crude/types/insight"
)

// SemanticTokens is passive, source-qualified evidence. The core owns the
// result ID and delta base; shells supply only their exact view sequence.
type SemanticTokens interface {
	RequestLSPSemantic(context.Context, insight.Source, string, uint64, insight.LSPSemanticRange) (insight.LSPSemanticSet, error)
	ReleaseLSPSemantic(context.Context, string, uint64) error
}
