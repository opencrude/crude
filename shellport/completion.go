package shellport

import (
	"context"

	"crude/types"
)

// Completions is the connected canonical-source flow for initial insertion
// evidence and documentation-only resolve. It has no shell-local fallback.
type Completions interface {
	RequestLSPCompletions(context.Context, types.InsightSource, string, uint64, int, int) (types.LSPCompletionSet, error)
	InspectLSPCompletion(context.Context, string, string, string, bool) (types.LSPCompletionInspection, error)
	ReleaseLSPCompletions(context.Context, string, uint64) error
}
