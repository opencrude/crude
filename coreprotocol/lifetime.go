package coreprotocol

import (
	"bytes"
	"errors"
	"time"

	"crude/workspace"
)

// Lifecycle control is deliberately outside the generated application schema.
// These bytes are frozen: one local control stream carries a request followed
// by a result and FIN. There is no Hello, session, application value or adapter.
const lifecycleStop = "CRUDE-STOP\x00"
const lifecycleResult = "CRUDE-STOP-RESULT\x00"
const LifecycleMaxMessage = 8192

func IsLifecycleStop(b []byte) bool { return bytes.HasPrefix(b, []byte(lifecycleStop)) }

func EncodeLifecycleStop(force bool, expectedOwner string) ([]byte, error) {
	if len(expectedOwner) > 256 {
		return nil, errors.New("invalid lifecycle owner")
	}
	b := append([]byte(lifecycleStop), 0)
	if force {
		b[len(b)-1] = 1
	}
	return append(b, expectedOwner...), nil
}

func DecodeLifecycleStop(b []byte) (ShutdownRequest, error) {
	if !IsLifecycleStop(b) || len(b) < len(lifecycleStop)+1 || len(b) > len(lifecycleStop)+257 || b[len(lifecycleStop)] > 1 {
		return ShutdownRequest{}, errors.New("invalid lifecycle stop request")
	}
	return ShutdownRequest{Force: b[len(lifecycleStop)] == 1, ExpectedOwner: string(b[len(lifecycleStop)+1:])}, nil
}

func EncodeLifecycleResult(accepted bool, reason string) []byte {
	b := append([]byte(lifecycleResult), 0)
	if accepted {
		b[len(b)-1] = 1
	}
	return append(b, reason[:min(len(reason), LifecycleMaxMessage-len(b))]...)
}

func DecodeLifecycleResult(b []byte) (ShutdownResult, error) {
	if !bytes.HasPrefix(b, []byte(lifecycleResult)) || len(b) < len(lifecycleResult)+1 || len(b) > LifecycleMaxMessage || b[len(lifecycleResult)] > 1 {
		return ShutdownResult{}, errors.New("invalid lifecycle stop result; shutdown outcome unknown")
	}
	return ShutdownResult{Accepted: b[len(lifecycleResult)] == 1, Reason: string(b[len(lifecycleResult)+1:])}, nil
}

// Core-level methods are not workspace-bound; the local endpoint's same-user
// boundary is their authorization.
const (
	CoreStatusMethod   = "core.status"
	CoreShutdownMethod = "core.shutdown"
	// CoreMetricsMethod takes a metrics.Query and returns the core process's
	// metrics.Snapshot for the Performance surface.
	CoreMetricsMethod = "core.metrics"
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
	// ObservationError means these counters could not be read on their owner.
	// Unknown work is busy for every lifetime decision, never an idle zero.
	ObservationError string `json:"observation_error,omitempty"`
}

// Busy reports active work or in-flight workspace operations.
func (w WorkspaceStatus) Busy() bool {
	return w.ObservationError != "" || w.Active.Any() || w.Operations > 0
}

type CoreStatus struct {
	PID int `json:"pid"`
	// BuildID identifies the core's executable; shells from another build
	// replace an idle core rather than speaking a mismatched protocol.
	BuildID string `json:"build_id,omitempty"`
	// Executable is the image the core runs from, a staged copy that never
	// pins the shell's own executable.
	Executable     string            `json:"executable,omitempty"`
	StartedAt      time.Time         `json:"started_at"`
	Server         bool              `json:"server"`
	GraceSeconds   float64           `json:"grace_seconds"`
	Clients        int               `json:"clients"`
	Opening        int               `json:"opening"`
	Draining       bool              `json:"draining"`
	Workspaces     []WorkspaceStatus `json:"workspaces"`
	Partial        bool              `json:"partial"`
	IdleForSeconds float64           `json:"idle_for_seconds"`
}

// ShutdownRequest asks the core to stop. Force overrides active work only.
type ShutdownRequest struct {
	Force bool `json:"force"`
	// ExpectedOwner binds a fixed old-image handover helper to one exact
	// endpoint lifetime; it cannot stop a replacement process on that address.
	ExpectedOwner string `json:"expected_owner,omitempty"`
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
