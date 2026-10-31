package shellport

import (
	"context"

	"crude/types"
)

// InlayHints is a connected read-only capability. Its retained original objects
// may provide tooltip evidence, never source mutation or executable adjuncts.
// It has no shell-local source or provider fallback.
type InlayHints interface {
	RequestLSPInlayHints(context.Context, types.InsightSource, string, uint64, types.LSPInlayRange) (types.LSPInlaySet, error)
	InspectLSPInlayHint(context.Context, string, string, int, bool) (types.LSPInlayInspection, error)
	ReleaseLSPInlayHints(context.Context, string, uint64) error
}
