package safety

type CRUDEOp string

const (
	OpCreate  CRUDEOp = "Create"
	OpRead    CRUDEOp = "Read"
	OpUpdate  CRUDEOp = "Update"
	OpDelete  CRUDEOp = "Delete"
	OpExecute CRUDEOp = "Execute"
)

func (op CRUDEOp) IsDestructive() bool {
	switch op {
	case OpCreate, OpUpdate, OpDelete, OpExecute:
		return true
	default:
		return false
	}
}

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

func CapabilityFromOp(op CRUDEOp) Capability {
	switch op {
	case OpCreate:
		return CanCreate
	case OpRead:
		return CanRead
	case OpUpdate:
		return CanUpdate
	case OpDelete:
		return CanDelete
	case OpExecute:
		return CanExecute
	default:
		return CanRead
	}
}

type Policy struct {
	AllowedCapabilities map[Capability]bool
	RequireConfirmation map[Capability]bool
	AllowRollback       bool
}

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

func ReadOnlyPolicy() Policy {
	return Policy{
		AllowedCapabilities: map[Capability]bool{
			CanRead: true,
		},
		RequireConfirmation: map[Capability]bool{},
		AllowRollback:       false,
	}
}

func (p Policy) IsAllowed(op CRUDEOp) bool {
	if len(p.AllowedCapabilities) == 0 {
		return true
	}
	return p.AllowedCapabilities[CapabilityFromOp(op)]
}

func (p Policy) NeedsConfirmation(op CRUDEOp) bool {
	return p.RequireConfirmation[CapabilityFromOp(op)]
}
