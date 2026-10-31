package shellport

import (
	"context"

	"crude/types"
)

// Navigation is a connected, read-only semantic capability. It neither saves
// source nor gives unversioned LSP destinations mutation or freshness authority.
type Navigation interface {
	RequestLSPNavigation(context.Context, types.InsightSource, string, int, int, bool) (types.LSPNavigation, error)
	InspectLSPNavigationTarget(context.Context, types.InsightBoundary, int, string, types.LSPNavigationTarget) (types.LSPNavigationLanding, error)
}
