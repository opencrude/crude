package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"crude/buildinfo"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/remoteconn"
	"crude/shellport/remote"
)

// remoteSSHDialer always authenticates a new session with system SSH. Bootstrap
// only learns authenticated endpoint/pin facts; the upgrade resumes that session
// with its resume secret, never creates a second owner with the one-use bearer.
func remoteSSHDialer(_ string, options corewire.SSHOptions) remote.Dialer {
	ssh := func(ctx context.Context) (corewire.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Handshake cancellation closes the mux and joins SSH. The handshake
		// context must not own the established process after Dial returns.
		conn, err := corewire.DialSSHBuild(context.Background(), options, buildinfo.DataDirName, buildinfo.ProtocolBuild)
		if err != nil {
			return nil, err
		}
		return corewire.NewMux(conn, corewire.CarrierSSH, true), nil
	}
	return func(ctx context.Context, clientID string) (*coreclient.Client, error) {
		client, err := coreclient.Dial(ctx, ssh, clientID)
		if err != nil {
			var fault *coreprotocol.Fault
			if errors.As(err, &fault) && fault.Code == "build_mismatch" || strings.Contains(err.Error(), "CRUDE_REMOTE_CORE_MISSING:") {
				return nil, prepareRemoteInstall(ctx, options, err)
			}
			return nil, fmt.Errorf("SSH core session failed: %w", err)
		}
		bootstrap, err := client.BootstrapRemoteSSH(ctx, "SSH shell")
		if err == nil {
			dial, valid := sshUpgradeDialer(options.Host, bootstrap, ssh)
			if valid == nil {
				// Upgrade failure is not session failure: SSH remains fully usable.
				// SwitchCarrier preserves the original owner/subscriptions/leases.
				_ = client.SwitchCarrier(ctx, dial)
			}
		}
		if err := ctx.Err(); err != nil {
			_ = client.Close()
			return nil, err
		}
		return client, nil
	}
}

// Wildcard listener addresses are not dial destinations. Resolve them relative
// to the original SSH host; a configured alias/ProxyJump may be unreachable
// directly, in which case the same verified SSH path remains the fallback.
func sshDirectAddress(address, sshHost string) (string, error) {
	if address == "" {
		return "", nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", errors.New("invalid SSH bootstrap address")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", errors.New("invalid SSH bootstrap port")
	}
	if host == "" || net.ParseIP(host) != nil && net.ParseIP(host).IsUnspecified() {
		host = sshHost
	}
	return net.JoinHostPort(host, port), nil
}

func sshUpgradeDialer(host string, bootstrap coreprotocol.RemoteSSHBootstrapResult, fallback coreclient.Dialer) (coreclient.Dialer, error) {
	pin := bootstrap.Credential.Fingerprint
	if !remoteconn.ValidFingerprint(pin) {
		return nil, errors.New("SSH bootstrap returned an invalid certificate pin")
	}
	quic, err := sshDirectAddress(bootstrap.QUICAddress, host)
	if err != nil {
		return nil, err
	}
	tls, err := sshDirectAddress(bootstrap.TLSAddress, host)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (corewire.Conn, error) {
		// Independent bounded attempts leave time for TCP and SSH when UDP is
		// blackholed. Both direct carriers enforce the same authenticated pin.
		for _, endpoint := range []struct {
			address string
			dial    func(context.Context, string, string) (corewire.Conn, error)
		}{{quic, corewire.DialQUIC}, {tls, corewire.DialTLS}} {
			if endpoint.address == "" {
				continue
			}
			attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
			conn, err := endpoint.dial(attempt, endpoint.address, pin)
			cancel()
			if err == nil {
				return conn, nil
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		return fallback(ctx)
	}, nil
}
