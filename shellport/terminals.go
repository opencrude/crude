package shellport

import (
	"crude/terminalvt"
	"crude/workspace"
)

// TerminalHost exposes the in-app PTY services.
type TerminalHost interface {
	// Terminals owns parser and reply state; nil until EnsureTerminals.
	Terminals() TerminalService
	// EnsureTerminals creates the terminal service on first use (error when no PTY manager exists).
	EnsureTerminals() (TerminalService, error)
	// TermMgr owns PTY sessions; nil when unavailable.
	TermMgr() TerminalManager
}

// TerminalService owns the one parser and reply state for every PTY.
type TerminalService interface {
	Acquire(id string, owner workspace.TerminalController) (workspace.TerminalLease, error)
	Close(id string)
	Create(opts workspace.TerminalCreateOpts) (workspace.TerminalInfo, error)
	Detach(owner workspace.TerminalController)
	Focus(lease workspace.TerminalLease, focused bool) error
	Key(lease workspace.TerminalLease, key terminalvt.Key) error
	Mouse(lease workspace.TerminalLease, mouse terminalvt.Mouse) error
	Paste(lease workspace.TerminalLease, data []byte) error
	Resize(lease workspace.TerminalLease, cols, rows int) error
	// Rows returns a detached bounded page; start=-1 requests the active grid.
	Rows(id string, start, count int) (workspace.TerminalProjection, error)
}

// TerminalManager owns the PTY sessions.
type TerminalManager interface {
	// Get returns one session snapshot.
	Get(id string) (workspace.TerminalInfo, bool)
	// List returns terminals in creation order.
	List() []workspace.TerminalInfo
}
