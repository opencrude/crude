package types

const (
	LSPNavigationDefinition     = "definition"
	LSPNavigationTypeDefinition = "type-definition"
	LSPNavigationImplementation = "implementation"
	LSPNavigationReferences     = "references"

	LSPNavigationTargets     = "targets"
	LSPNavigationNull        = "null"
	LSPNavigationEmpty       = "empty"
	LSPNavigationUnsupported = "unsupported"
)

// LSPNavigationResponse is detached internal transport evidence. The manager,
// not the adapter or caller, qualifies Boundary after its lifecycle checks.
// Unsupported is a statically observed missing provider, not an empty answer.
type LSPNavigationResponse struct {
	Boundary    InsightBoundary
	WireVersion int
	Kind        string
	JSON        []byte
	Unsupported bool
}

// Navigation positions are zero-based UTF-16, never terminal display columns.
type LSPNavigationPosition struct {
	Line, Character int
}

type LSPNavigationRange struct {
	Start, End LSPNavigationPosition
}

// LSPNavigationTarget preserves LocationLink metadata. Path and availability
// come from the core's strict local-file URI policy, not a shell URI decoder.
// Standard LSP locations carry no target version. A usable row is not evidence
// that the provider analyzed the current bytes of its destination.
type LSPNavigationTarget struct {
	URI, Path, UnavailableReason string
	Range, Selection             LSPNavigationRange
	Origin                       *LSPNavigationRange
	Link, External               bool
}

// LSPNavigation is one source/provider/context-qualified answer. Null, empty
// and unsupported answers retain the same boundary and invocation coordinates.
// Errors return no answer. Targets do not include file contents or authority to
// mutate, execute, or silently read all reported destinations.
type LSPNavigation struct {
	Boundary           InsightBoundary
	WireVersion        int
	Kind, Answer       string
	Line, Col          int
	IncludeDeclaration bool
	Targets            []LSPNavigationTarget
}

// LSPNavigationLanding is an explicit selected-target observation. Target
// ranges were checked against these bytes off the UI thread. A later open must
// match TextHash and, for a canonical observation, Source before positioning.
// This structural/content check never supplies a missing LSP target version.
type LSPNavigationLanding struct {
	Boundary InsightBoundary
	Target   LSPNavigationTarget
	Source   InsightSource
	TextHash string
	Preview  string
}
