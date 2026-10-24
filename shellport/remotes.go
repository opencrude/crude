package shellport

import (
	"context"
	"time"

	"crude/remoteconn"
)

// RemoteManager is client-owned, not a service on the selected core. All methods
// may do I/O and must run off the shell's Update goroutine. Credentials never
// appear in snapshots or results.
type RemoteManager interface {
	Snapshot(context.Context) (remoteconn.Snapshot, error)
	Save(context.Context, RemoteConnectionDraft) (RemoteStatus, error)
	Test(context.Context, remoteconn.Connection) (RemoteStatus, error)
	Remove(context.Context, string) error
	Attach(context.Context, string, string) (Workspace, error)
	RecordUse(context.Context, string, string) error
}

type RemoteConnectionDraft struct {
	Connection        remoteconn.Connection
	PairCode          string
	AllowFileFallback bool
}

func (RemoteConnectionDraft) String() string     { return "[remote connection draft]" }
func (v RemoteConnectionDraft) GoString() string { return v.String() }

type RemoteStatus struct {
	State   string // connected, reconnecting, offline, needs install
	Carrier string
	RTT     time.Duration
	Notice  string
}

// BackendLink projects the attached SDK session without network work on Update.
type BackendLink interface{ BackendStatus() RemoteStatus }

// SSHConfigHostSource offers literal aliases from this client's SSH config.
// System ssh remains authoritative for Include, Match and ProxyJump at dial.
type SSHConfigHostSource interface {
	SSHConfigHosts(context.Context) ([]string, error)
}

// RemoteInstallOffer is an immutable, destination-specific consent challenge.
// Merely receiving an offer performs no installation. The shell must obtain
// explicit approval and fence the exact form and request before calling it.
type RemoteInstallOffer interface {
	error
	RemoteInstallSummary() string
	InstallRemote(context.Context) error
}
