package coreprotocol

import "crude/types"

const (
	LSPNavigationMethod       = "lsp.navigation"
	LSPNavigationTargetMethod = "lsp.navigation_target"
)

type NavigationRequest struct {
	Source             types.InsightSource
	Kind               string
	Line, Col          int
	IncludeDeclaration bool
}

// Selected-target operands are inert reads. The core revalidates the original
// source and producer and re-derives URI policy before observing any target.
type NavigationTargetRequest struct {
	Boundary    types.InsightBoundary
	WireVersion int
	Kind        string
	Target      types.LSPNavigationTarget
}

type NavigationReply struct {
	Result  types.LSPNavigation
	Landing types.LSPNavigationLanding
}
