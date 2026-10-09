package coreprotocol

import "crude/workspace"

// InvocationSelection is write-only input to session admission. It must never
// be echoed in responses, snapshots, diagnostics or durable request payloads.
// Instructions extend the lower-priority profile; canonical policy is unchanged.
type InvocationSelection = workspace.InvocationSelection

type InvocationSessionRequest struct {
	SessionID string              `json:"session_id,omitempty"`
	Selection InvocationSelection `json:"selection,omitempty"`
}
