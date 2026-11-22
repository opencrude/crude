package shellport

import (
	"context"

	"crude/types/insight"
)

// InlayHints is a connected read-only capability. Its retained original objects
// may provide tooltip evidence, never source mutation or executable adjuncts.
// It has no shell-local source or provider fallback.
type InlayHints interface {
	RequestLSPInlayHints(context.Context, insight.Source, string, uint64, insight.LSPInlayRange) (insight.LSPInlaySet, error)
	InspectLSPInlayHint(context.Context, string, string, int, bool) (insight.LSPInlayInspection, error)
	ReleaseLSPInlayHints(context.Context, string, uint64) error
}
