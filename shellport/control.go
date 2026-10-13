package shellport

import (
	"context"

	"crude/coreprotocol"
	"crude/types/control"
)

// ShellControl is an exact connected attachment, not a control grant. Start is
// passive and nonblocking; registration and replies perform RPC off Update.
type ShellControl interface {
	ControlIdentity() control.Target
	StartShellControl()
	StopShellControl()
	ReplyShellControl(context.Context, control.Result) error
}

// ControlRequestMsg enters the UI FIFO separately from coalescible replica data.
// Owner fences workspace replacement; Envelope fences the connection epoch.
type ControlRequestMsg struct {
	Owner    ShellControl
	Envelope coreprotocol.ControlEnvelope
	// Reply is captured by the receiving connection. Reconnect must never send
	// an old request's outcome through a replacement attachment.
	Reply func(context.Context, control.Result) error
}
