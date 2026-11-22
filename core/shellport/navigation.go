package shellport

import (
	"context"

	"crude/types/insight"
)

// Navigation is a connected, read-only semantic capability. It neither saves
// source nor gives unversioned LSP destinations mutation or freshness authority.
type Navigation interface {
	RequestLSPNavigation(context.Context, insight.Source, string, int, int, bool) (insight.LSPNavigation, error)
	InspectLSPNavigationTarget(context.Context, insight.Boundary, int, string, insight.LSPNavigationTarget) (insight.LSPNavigationLanding, error)
}
