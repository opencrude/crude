package safety

import "crude/dag"

// Capability represents a permission to perform a CRUDE operation.
type Capability int

const (
	CanCreate Capability = iota
	CanRead
	CanUpdate
	CanDelete
	CanExecute
)

func (c Capability) String() string {
	switch c {
	case CanCreate:
		return "create"
	case CanRead:
		return "read"
	case CanUpdate:
		return "update"
	case CanDelete:
		return "delete"
	case CanExecute:
		return "execute"
	default:
		return "unknown"
	}
}

// CapabilityFromOp maps a CRUDE operation to its required capability.
func CapabilityFromOp(op dag.CRUDEOp) Capability {
	switch op {
	case dag.OpCreate:
		return CanCreate
	case dag.OpRead:
		return CanRead
	case dag.OpUpdate:
		return CanUpdate
	case dag.OpDelete:
		return CanDelete
	case dag.OpExecute:
		return CanExecute
	default:
		return CanRead
	}
}

// Policy defines what operations are permitted and which require confirmation.
type Policy struct {
	// AllowedCapabilities is the set of operations this backend can perform.
	// If empty, all operations are allowed.
	AllowedCapabilities map[Capability]bool

	// RequireConfirmation lists operations that need user confirmation before executing.
	RequireConfirmation map[Capability]bool

	// AllowRollback enables undo support for mutating operations.
	AllowRollback bool
}

// DefaultPolicy returns a policy that allows all operations,
// requires confirmation for Delete, and enables rollback.
func DefaultPolicy() Policy {
	return Policy{
		AllowedCapabilities: map[Capability]bool{
			CanCreate:  true,
			CanRead:    true,
			CanUpdate:  true,
			CanDelete:  true,
			CanExecute: true,
		},
		RequireConfirmation: map[Capability]bool{
			CanDelete: true,
		},
		AllowRollback: true,
	}
}

// ReadOnlyPolicy returns a policy that only allows Read operations.
func ReadOnlyPolicy() Policy {
	return Policy{
		AllowedCapabilities: map[Capability]bool{
			CanRead: true,
		},
		RequireConfirmation: map[Capability]bool{},
		AllowRollback:       false,
	}
}

// IsAllowed returns true if the given operation is permitted by this policy.
func (p Policy) IsAllowed(op dag.CRUDEOp) bool {
	if len(p.AllowedCapabilities) == 0 {
		return true
	}
	return p.AllowedCapabilities[CapabilityFromOp(op)]
}

// NeedsConfirmation returns true if the given operation requires user confirmation.
func (p Policy) NeedsConfirmation(op dag.CRUDEOp) bool {
	return p.RequireConfirmation[CapabilityFromOp(op)]
}
