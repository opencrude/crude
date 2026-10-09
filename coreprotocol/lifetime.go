package coreprotocol

import (
	"time"

	"crude/workspace"
)

// Core-level methods are not workspace-bound; the local endpoint's same-user
// boundary is their authorization.
const (
	CoreStatusMethod   = "core.status"
	CoreShutdownMethod = "core.shutdown"
)

// ActiveWork is the per-workspace work a shutdown must not silently abandon.
type ActiveWork = workspace.ActiveWork

type WorkspaceStatus struct {
	ID         string     `json:"id"`
	Root       string     `json:"root"`
	Agents     int        `json:"agents"`
	Sessions   int        `json:"sessions"`
	Operations int        `json:"operations"`
	Active     ActiveWork `json:"active"`
}

// Busy reports active work or in-flight workspace operations.
func (w WorkspaceStatus) Busy() bool { return w.Active.Any() || w.Operations > 0 }

type CoreStatus struct {
	PID int `json:"pid"`
	// BuildID identifies the core's executable; shells from another build
	// replace an idle core rather than speaking a mismatched protocol.
	BuildID        string            `json:"build_id,omitempty"`
	StartedAt      time.Time         `json:"started_at"`
	Server         bool              `json:"server"`
	GraceSeconds   float64           `json:"grace_seconds"`
	Clients        int               `json:"clients"`
	Opening        int               `json:"opening"`
	Draining       bool              `json:"draining"`
	Workspaces     []WorkspaceStatus `json:"workspaces"`
	IdleForSeconds float64           `json:"idle_for_seconds"`
}

// ShutdownRequest asks the core to stop. Force overrides active work only.
type ShutdownRequest struct {
	Force bool `json:"force"`
}

// ShutdownResult reports the decision. A refusal is a typed result, not a fault,
// so callers can show exactly which work is active.
type ShutdownResult struct {
	Accepted bool              `json:"accepted"`
	Reason   string            `json:"reason,omitempty"`
	Busy     []WorkspaceStatus `json:"busy,omitempty"`
	// Shells counts other attached clients a stop would disconnect.
	Shells int `json:"shells,omitempty"`
}
