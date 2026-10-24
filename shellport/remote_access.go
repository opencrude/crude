package shellport

import (
	"context"

	"crude/coreprotocol"
)

// RemoteAccessAdmin is an optional application-host service, not workspace
// authority. Calls perform I/O and must run off the shell's Update goroutine.
// The core must independently enforce local-host authority for every operation.
type RemoteAccessAdmin interface {
	RemoteAccess(context.Context) (coreprotocol.RemoteAccessView, error)
	CreateRemotePairing(context.Context) (coreprotocol.RemotePairingCode, error)
	RevokeRemoteDevice(context.Context, string) error
}

// LocalHostAuthority reports immutable negotiated admission provenance, never a
// guess from the chosen transport, a loopback tunnel or a backend label.
type LocalHostAuthority interface{ LocalHost() bool }
