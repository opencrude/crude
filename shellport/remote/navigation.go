package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
)

var _ shellport.Navigation = (*Workspace)(nil)

func (w *Workspace) RequestLSPNavigation(ctx context.Context, source types.InsightSource, kind string, line, col int, includeDeclaration bool) (types.LSPNavigation, error) {
	var out coreprotocol.NavigationReply
	err := w.callCancellable(ctx, coreprotocol.LSPNavigationMethod, &coreprotocol.NavigationRequest{
		Source: source, Kind: kind, Line: line, Col: col, IncludeDeclaration: includeDeclaration,
	}, &out)
	return out.Result, err
}

func (w *Workspace) InspectLSPNavigationTarget(ctx context.Context, boundary types.InsightBoundary, wire int, kind string, target types.LSPNavigationTarget) (types.LSPNavigationLanding, error) {
	var out coreprotocol.NavigationReply
	err := w.callCancellable(ctx, coreprotocol.LSPNavigationTargetMethod, &coreprotocol.NavigationTargetRequest{
		Boundary: boundary, WireVersion: wire, Kind: kind, Target: target,
	}, &out)
	return out.Landing, err
}
