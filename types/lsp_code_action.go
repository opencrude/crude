package types

// LSPCodeActionResponse is internal adapter-to-workspace evidence, not a shell
// DTO or permission to execute an action. The manager supplies Boundary only
// after checking canonical source, provider and context authority. JSON retains
// the original provider tokens; callers own their detached copy.
type LSPCodeActionResponse struct {
	Boundary            InsightBoundary
	WireVersion         int
	JSON                []byte
	ResolveSupported    bool
	DiagnosticsIncluded int
	DiagnosticsDetail   string
}
