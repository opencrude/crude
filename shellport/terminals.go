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
	Rows(id string, request terminalvt.RowRequest) (workspace.TerminalProjection, error)
}

// StreamingTerminals owns asynchronous grid publication and nonblocking ordered
// input admission. Unlike Rows, StartGrid performs no I/O on the caller thread.
type StreamingTerminals interface {
	StartGrid(id string)
	SetViewport(id string, request terminalvt.RowRequest)
	SendInput(workspace.TerminalInput) error
	// ApplyTerminalFrame validates exact stream ownership and installs replica
	// caches on Update. It may supply the latest authoritative error rollback.
	// Rejection must leave both the message and replica unchanged.
	ApplyTerminalFrame(*TerminalFrameMsg) error
}

// TerminalFrameMsg is installed on the shell's Update thread, never by the
// subscription worker. Service identity fences workspace switches.
type TerminalFrameMsg struct {
	Service    TerminalService
	ID         string
	Projection workspace.TerminalProjection
	Err        error
	// Frame is the service's opaque stream-generation/input owner token.
	Frame any
	// Applied is a buffered, one-result application receipt. Update sends nil
	// only after installation; stale/closed owners receive cancellation instead.
	Applied chan<- error
}

// TerminalManager owns the PTY sessions.
type TerminalManager interface {
	// Get returns one session snapshot.
	Get(id string) (workspace.TerminalInfo, bool)
	// List returns terminals in creation order.
	List() []workspace.TerminalInfo
}
