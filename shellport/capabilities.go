package shellport

import (
	"context"

	captypes "crude/types/capability"
)

// Capabilities is the skills, MCP and acquisition surface.
type Capabilities interface {
	CapabilityAction(ctx context.Context, action captypes.Action) (captypes.Snapshot, []captypes.Entry, string, error)
	// CapabilityRuntimeSnapshot observes already initialized live state.
	// Unlike CapabilitySnapshot it does not reload settings, inspect skill files,
	// or reconfigure transports; presentation polling must never do that work.
	CapabilityRuntimeSnapshot(ctx context.Context) (captypes.Snapshot, error)
	CapabilitySnapshot(ctx context.Context) (captypes.Snapshot, error)
}
