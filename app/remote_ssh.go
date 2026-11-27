package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/core/corewire"
	"crude/core/remoteconn"
	"crude/core/shellport/remote"
	"crude/internal/buildinfo"
)

// remoteSSHDialer always authenticates a new session with system SSH and
// returns it as soon as SSH carries it. The direct-carrier upgrade then runs in
// the background: bootstrap only learns authenticated
// endpoint/pin facts, and the upgrade resumes that session with its resume
// secret, never creating a second owner with the one-use bearer. An endpoint
// that does not answer, such as a loopback-only listener on a distinct host,
// does not add another handshake to the launch path.
func remoteSSHDialer(_ string, options corewire.SSHOptions) remote.Dialer {
	ssh := sshCarrier(options)
	return func(ctx context.Context, clientID string) (*coreclient.Client, error) {
		client, err := coreclient.Dial(ctx, ssh, clientID)
		if err != nil {
			var fault *coreprotocol.Fault
			if errors.As(err, &fault) && fault.Code == "build_mismatch" || strings.Contains(err.Error(), "CRUDE_REMOTE_CORE_MISSING:") {
				return nil, prepareRemoteInstall(ctx, options, err)
			}
			return nil, fmt.Errorf("SSH core session failed: %w", err)
		}
		// Upgrade failure is not session failure: SSH remains fully usable.
		// SwitchCarrier preserves the original owner/subscriptions/leases.
		client.UpgradeInBackground(func(ctx context.Context) (coreclient.Dialer, coreclient.Dialer, error) {
			bootstrap, err := client.BootstrapRemoteSSH(ctx, "SSH shell")
			if err != nil {
				return nil, nil, err
			}
			direct, err := sshDirectDialer(options.Host, bootstrap)
			if err != nil {
				return nil, nil, err
			}
			return direct, sshUpgradeDialer(direct, ssh), nil
		})
		return client, nil
	}
}

// sshCarrier starts the remote core's stdio relay through system SSH.
func sshCarrier(options corewire.SSHOptions) coreclient.Dialer {
	return func(ctx context.Context) (corewire.Conn, error) {
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

// sshUpgradeDialer is the reconnect policy of an upgraded SSH session: the
// pinned direct carriers, then the verified SSH path. The upgrade attempt
// itself dials only the direct carriers: SSH already carries the session, and
// a second SSH handshake only costs the user time.
func sshUpgradeDialer(direct, fallback coreclient.Dialer) coreclient.Dialer {
	return func(ctx context.Context) (corewire.Conn, error) {
		conn, err := direct(ctx)
		if err == nil {
			return conn, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return fallback(ctx)
	}
}

// sshDirectDialer dials the bootstrap's pinned QUIC endpoint, then its pinned
// TLS endpoint, each with an independent bounded attempt.
func sshDirectDialer(host string, bootstrap coreprotocol.RemoteSSHBootstrapResult) (coreclient.Dialer, error) {
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
	if quic == "" && tls == "" {
		return nil, errors.New("SSH bootstrap returned no direct endpoint")
	}
	return func(ctx context.Context) (corewire.Conn, error) {
		// Independent bounded attempts leave time for TCP and SSH when UDP is
		// blackholed. Both direct carriers enforce the same authenticated pin.
		var failure error
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
			failure = err
		}
		return nil, failure
	}, nil
}
