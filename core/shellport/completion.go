package shellport

import (
	"context"

	"crude/types/insight"
)

// Completions is the connected canonical-source flow for initial insertion
// evidence and documentation-only resolve. It has no shell-local fallback.
type Completions interface {
	RequestLSPCompletions(context.Context, insight.Source, string, uint64, int, int, insight.LSPCompletionContext) (insight.LSPCompletionSet, error)
	InspectLSPCompletion(context.Context, string, string, string, bool) (insight.LSPCompletionInspection, error)
	ReleaseLSPCompletions(context.Context, string, uint64) error
}
