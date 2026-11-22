package coreprotocol

import (
	"crude/types/inspect"
)

// Core diagnostic RPCs remain usable before a workspace attachment; semantic
// native/MCP access still goes through its existing caller/grant gateway.
const (
	CorePerformanceInspect = "core.performance.inspect"
	CoreDiagnosticsInspect = "core.diagnostics.inspect"
)

type PerformanceInspectRequest struct {
	Page    inspect.Request `json:"page"`
	Seconds int             `json:"seconds,omitempty"`
	Points  int             `json:"points,omitempty"`
}

type DiagnosticsInspectRequest struct {
	Page inspect.Request `json:"page"`
}
