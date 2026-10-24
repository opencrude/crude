package coreclient

import (
	"context"
	"errors"
	"time"

	"crude/buildinfo"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/remoteconn"
)

// PairRemote enrolls exactly one device over pinned TLS/QUIC without creating a
// session. The caller must first display and obtain explicit confirmation of the
// fingerprint through the pairing UX; this function never silently trusts a
// certificate it discovers. No unpinned dialer is accepted and pairing is never
// retried automatically (a lost response needs a new host-generated code).
// carrier must be quic or tls; the pin and code are used identically on both.
func PairRemote(ctx context.Context, address, fingerprint, carrier, clientID string, request coreprotocol.RemotePairRequest) (coreprotocol.RemoteCredential, error) {
	var none coreprotocol.RemoteCredential
	if !remoteconn.ValidFingerprint(fingerprint) {
		return none, errors.New("core client: pairing requires a confirmed fingerprint")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var conn corewire.Conn
	var err error
	switch carrier {
	case "quic":
		conn, err = corewire.DialQUIC(ctx, address, fingerprint)
	case "tls":
		conn, err = corewire.DialTLS(ctx, address, fingerprint)
	default:
		return none, errors.New("core client: pairing requires quic or tls")
	}
	if err != nil {
		return none, err
	}
	defer func() { _ = conn.Close() }()
	control, err := conn.OpenStream(ctx, corewire.ClassControl)
	if err != nil {
		return none, err
	}
	stop := context.AfterFunc(ctx, func() { _ = control.Close() })
	defer stop()
	defer func() { _ = control.Close() }()
	hello := coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: clientID, Pair: &request}
	if err = sendValues(control, &hello); err != nil {
		return none, err
	}
	body, err := corewire.NewReader(control, 64<<10).Next()
	if err != nil {
		return none, err
	}
	var welcome coreprotocol.Welcome
	if err = coreprotocol.Decode(body, &welcome); err != nil {
		return none, err
	}
	if welcome.Fault != nil {
		return none, welcome.Fault
	}
	if welcome.Schema != coreprotocol.Schema || welcome.Build != buildinfo.ProtocolBuild || welcome.Paired == nil || welcome.Paired.Fingerprint != fingerprint || welcome.SessionID != "" || welcome.Secret != "" || welcome.Owner.Epoch != 0 {
		return none, errors.New("core client: invalid pairing response")
	}
	if _, _, ok := coreprotocol.SplitRemoteBearer(welcome.Paired.Bearer()); !ok {
		return none, errors.New("core client: invalid device credential")
	}
	if err := control.CloseWrite(); err != nil {
		return none, err
	}
	return *welcome.Paired, nil
}

// These host operations do not require an attached workspace. The core transport
// must enforce local-host authority for pairing/revocation and verified SSH for
// bootstrap; an ordinary remote device cannot administer its own admission.
func (c *Client) RemoteAccess(ctx context.Context) (coreprotocol.RemoteAccessView, error) {
	var out coreprotocol.RemoteAccessView
	err := c.Call(ctx, "", coreprotocol.RemoteAccessRead, &coreprotocol.Empty{}, &out)
	return out, err
}
func (c *Client) CreateRemotePairing(ctx context.Context) (coreprotocol.RemotePairingCode, error) {
	var out coreprotocol.RemotePairingCode
	err := c.Call(ctx, "", coreprotocol.RemotePairingCreate, &coreprotocol.Empty{}, &out)
	return out, err
}
func (c *Client) RevokeRemoteDevice(ctx context.Context, id string) error {
	var out coreprotocol.Empty
	return c.Call(ctx, "", coreprotocol.RemoteDeviceRevoke, &coreprotocol.RemoteRevokeRequest{DeviceID: id}, &out)
}
func (c *Client) BootstrapRemoteSSH(ctx context.Context, name string) (coreprotocol.RemoteSSHBootstrapResult, error) {
	var out coreprotocol.RemoteSSHBootstrapResult
	err := c.Call(ctx, "", coreprotocol.RemoteSSHBootstrap, &coreprotocol.RemoteSSHBootstrapRequest{DeviceName: name}, &out)
	return out, err
}
