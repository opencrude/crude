package coreprotocol

import "crude/workspace"

// InvocationSessionRequest asks for the invocation of an editor selection in a session.
type InvocationSessionRequest struct {
	SessionID string                        `json:"session_id,omitempty"`
	Selection workspace.InvocationSelection `json:"selection,omitempty"`
}
