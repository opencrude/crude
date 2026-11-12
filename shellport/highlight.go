package shellport

import (
	"context"

	"crude/types"
)

// Highlights is a connected, read-only semantic capability: occurrences of the
// symbol at one caret, for presentation only.
type Highlights interface {
	RequestLSPHighlights(context.Context, types.InsightSource, int, int) (types.LSPHighlights, error)
}
