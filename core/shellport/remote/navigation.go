package remote

import (
	"context"

	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/types/insight"
)

var _ shellport.Navigation = (*Workspace)(nil)

// RequestLSPNavigation resolves a definition, reference or other navigation request.
func (w *Workspace) RequestLSPNavigation(ctx context.Context, source insight.Source, kind string, line, col int, includeDeclaration bool) (insight.LSPNavigation, error) {
	var out coreprotocol.NavigationReply
	err := w.callCancellable(ctx, coreprotocol.LSPNavigationMethod, &coreprotocol.NavigationRequest{
		Source: source, Kind: kind, Line: line, Col: col, IncludeDeclaration: includeDeclaration,
	}, &out)
	return out.Result, err
}

// InspectLSPNavigationTarget checks a navigation target and returns where it lands.
func (w *Workspace) InspectLSPNavigationTarget(ctx context.Context, boundary insight.Boundary, wire int, kind string, target insight.LSPNavigationTarget) (insight.LSPNavigationLanding, error) {
	var out coreprotocol.NavigationReply
	err := w.callCancellable(ctx, coreprotocol.LSPNavigationTargetMethod, &coreprotocol.NavigationTargetRequest{
		Boundary: boundary, WireVersion: wire, Kind: kind, Target: target,
	}, &out)
	return out.Landing, err
}
