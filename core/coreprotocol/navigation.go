package coreprotocol

import (
	"crude/types/insight"
)

const (
	LSPNavigationMethod       = "lsp.navigation"
	LSPNavigationTargetMethod = "lsp.navigation_target"
)

type NavigationRequest struct {
	Source             insight.Source
	Kind               string
	Line, Col          int
	IncludeDeclaration bool
}

// Selected-target operands are inert reads. The core revalidates the original
// source and producer and re-derives URI policy before observing any target.
type NavigationTargetRequest struct {
	Boundary    insight.Boundary
	WireVersion int
	Kind        string
	Target      insight.LSPNavigationTarget
}

type NavigationReply struct {
	Result  insight.LSPNavigation
	Landing insight.LSPNavigationLanding
}
