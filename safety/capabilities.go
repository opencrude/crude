package safety

import (
	"sync"

	"crude/logger"
)

var (
	pkgLogger   *logger.Logger
	pkgLoggerMu sync.RWMutex
)

func SetCapabilitiesLogger(lg *logger.Logger) {
	pkgLoggerMu.Lock()
	defer pkgLoggerMu.Unlock()
	pkgLogger = lg
}

func safetyLogIf(fn func(lg *logger.Logger)) {
	pkgLoggerMu.RLock()
	lg := pkgLogger
	pkgLoggerMu.RUnlock()
	if lg != nil {
		fn(lg)
	}
}

type CRUDEOp string

const (
	OpCreate          CRUDEOp = "Create"
	OpRead            CRUDEOp = "Read"
	OpUpdate          CRUDEOp = "Update"
	OpDelete          CRUDEOp = "Delete"
	OpExecute         CRUDEOp = "Execute"
	OpExecuteReadOnly CRUDEOp = "ExecuteReadOnly"
)

func (op CRUDEOp) IsDestructive() bool {
	switch op {
	case OpCreate, OpUpdate, OpDelete, OpExecute:
		return true
	case OpExecuteReadOnly:
		return false
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
	CanExecuteReadOnly
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
	case CanExecuteReadOnly:
		return "execute-read-only"
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
	case OpExecuteReadOnly:
		return CanExecuteReadOnly
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
			CanCreate:          true,
			CanRead:            true,
			CanUpdate:          true,
			CanDelete:          true,
			CanExecute:         true,
			CanExecuteReadOnly: true,
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
			CanRead:            true,
			CanExecuteReadOnly: true,
		},
		RequireConfirmation: map[Capability]bool{},
		AllowRollback:       false,
	}
}

func (p Policy) IsAllowed(op CRUDEOp) bool {
	if len(p.AllowedCapabilities) == 0 {
		safetyLogIf(func(lg *logger.Logger) { lg.Warn("SAFETY", "empty_policy_allow_all", "op", op) })
		return true
	}
	allowed := p.AllowedCapabilities[CapabilityFromOp(op)]
	safetyLogIf(func(lg *logger.Logger) { lg.Debug("SAFETY", "policy_check", "op", op, "allowed", allowed) })
	return allowed
}

func (p Policy) NeedsConfirmation(op CRUDEOp) bool {
	needed := p.RequireConfirmation[CapabilityFromOp(op)]
	safetyLogIf(func(lg *logger.Logger) { lg.Debug("SAFETY", "needs_confirmation", "op", op, "needed", needed) })
	return needed
}
