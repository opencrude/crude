package shellport

import (
	"context"

	"crude/types/insight"
)

// Highlights is a connected, read-only semantic capability: occurrences of the
// symbol at one caret, for presentation only.
type Highlights interface {
	RequestLSPHighlights(context.Context, insight.Source, int, int) (insight.LSPHighlights, error)
}
