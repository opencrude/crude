package app

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/network"
	"crude/workspace"
)

func remoteHostSettings(t *testing.T) network.Settings {
	t.Helper()
	s := network.DefaultSettings()
	// Reserve together so the two probes cannot receive the same port.
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		_ = tcp.Close()
		t.Fatal(err)
	}
	s.RemoteTLSAddress, s.RemoteQUICAddress = tcp.Addr().String(), udp.LocalAddr().String()
	_ = tcp.Close()
	_ = udp.Close()
	return s
}

func remoteHostRuntime(t *testing.T) *core.Runtime {
	t.Helper()
	r, err := core.New(func(string) (*workspace.Workspace, error) { return nil, errors.New("unexpected workspace creation") }, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func remoteHostFixture(t *testing.T, settings network.Settings) (*coreRemote, *core.Runtime) {
	t.Helper()
	r := remoteHostRuntime(t)
	h, err := newCoreRemote(t.Context(), r, filepath.Join(t.TempDir(), "remote"), settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h, r
}

func remoteHostDial(t *testing.T, dial coreclient.Dialer, credential string) *coreclient.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := coreclient.DialWithOptions(ctx, dial, core.NewClientID(), coreclient.DialOptions{Credential: credential})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func remoteHostStatus(t *testing.T, client *coreclient.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var out coreprotocol.CoreStatus
	if err := client.Call(ctx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, &out); err != nil {
		t.Fatal(err)
	}
}

func TestCoreRemoteDisabledProfilesIgnoreOccupiedRequestedPorts(t *testing.T) {
	s := network.DefaultSettings()
	// An existing core or unrelated service may own the default ports. Neither
	// case should make a new disabled private profile need remote-access repair.
	tcp, err := net.Listen("tcp", s.RemoteTLSAddress)
	if err == nil {
		defer func() { _ = tcp.Close() }()
	} else if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp", s.RemoteQUICAddress)
	if err == nil {
		defer func() { _ = udp.Close() }()
	} else if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatal(err)
	}
	first, _ := remoteHostFixture(t, s)
	second, _ := remoteHostFixture(t, s)
	for _, h := range []*coreRemote{first, second} {
		if h.Err() != nil {
			t.Fatal(h.Err())
		}
		boot, err := h.access.BootstrapSSH(t.Context(), "private SSH")
		if err != nil {
			t.Fatal(err)
		}
		for _, address := range []string{boot.QUICAddress, boot.TLSAddress} {
			host, port, err := net.SplitHostPort(address)
			if err != nil || host != "127.0.0.1" || port == "0" || port == "" || port == "7340" {
				t.Fatalf("disabled profile advertised non-private endpoint %q: %v", address, err)
			}
		}
		view, err := h.access.View(t.Context())
		if err != nil || view.Config.QUICAddress != s.RemoteQUICAddress || view.Config.TLSAddress != s.RemoteTLSAddress || view.Config.Enabled {
			t.Fatalf("requested settings changed: %+v %v", view, err)
		}
	}
	if first.quic.address == second.quic.address || first.tls.address == second.tls.address {
		t.Fatal("independent profiles share listener addresses")
	}
}

func TestCoreRemoteDisabledAddressIntentAndEnableConflict(t *testing.T) {
	s := network.DefaultSettings()
	s.RemoteQUICAddress, s.RemoteTLSAddress = "0.0.0.0:7340", "[::]:7340"
	h, _ := remoteHostFixture(t, s)
	oldQUIC, oldTLS := h.quic, h.tls
	for _, endpoint := range []*remoteEndpoint{oldQUIC, oldTLS} {
		host, port, err := net.SplitHostPort(endpoint.address)
		if err != nil || host != "127.0.0.1" || port == "0" || endpoint.bindAddress != "127.0.0.1:0" {
			t.Fatalf("cold disabled wildcard bound %q (key %q): %v", endpoint.address, endpoint.bindAddress, err)
		}
	}
	blocked, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocked.Close() }()
	next := remoteHostSettings(t)
	next.RemoteTLSAddress = blocked.Addr().String()
	for _, settings := range []network.Settings{s, next} {
		change, err := h.Prepare(settings)
		if err != nil {
			t.Fatal(err)
		}
		if change.newQUIC || change.newTLS {
			t.Fatal("disabled settings rebound healthy private listeners")
		}
		if err := change.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if h.quic != oldQUIC || h.tls != oldTLS {
		t.Fatal("disabled edit changed endpoint identity")
	}
	view, err := h.access.View(t.Context())
	if err != nil || view.Config.TLSAddress != next.RemoteTLSAddress || view.Config.QUICAddress != next.RemoteQUICAddress || view.Config.Enabled {
		t.Fatalf("disabled intent not published: %+v %v", view, err)
	}
	next.RemoteAccessEnabled = true
	if _, err := h.Prepare(next); err == nil {
		t.Fatal("enable accepted occupied configured TCP port")
	}
	view, err = h.access.View(t.Context())
	if err != nil || view.Config.Enabled || h.quic != oldQUIC || h.tls != oldTLS {
		t.Fatalf("failed enable replaced disabled state: %+v %v", view, err)
	}
	boot, err := h.access.BootstrapSSH(t.Context(), "old remains usable")
	if err != nil || boot.QUICAddress != oldQUIC.address || boot.TLSAddress != oldTLS.address {
		t.Fatalf("failed enable lost private endpoints: %+v %v", boot, err)
	}
	client := remoteHostDial(t, func(ctx context.Context) (corewire.Conn, error) {
		return corewire.DialTLS(ctx, boot.TLSAddress, boot.Credential.Fingerprint)
	}, boot.Credential.Bearer())
	remoteHostStatus(t, client)
	udp, err := net.ListenPacket("udp", next.RemoteQUICAddress)
	if err != nil {
		t.Fatalf("failed enable leaked new UDP reservation: %v", err)
	}
	_ = udp.Close()
}

func TestCoreRemoteEnableActualPrivateAddressCommitsReuseKey(t *testing.T) {
	h, _ := remoteHostFixture(t, network.DefaultSettings())
	oldQUIC, oldTLS := h.quic, h.tls
	next := network.DefaultSettings()
	next.RemoteAccessEnabled = true
	next.RemoteQUICAddress, next.RemoteTLSAddress = oldQUIC.address, oldTLS.address
	change, err := h.Prepare(next)
	if err != nil {
		t.Fatal(err)
	}
	if change.newQUIC || change.newTLS || oldQUIC.bindAddress != "127.0.0.1:0" || oldTLS.bindAddress != "127.0.0.1:0" {
		t.Fatal("Prepare rebound or mutated live endpoint reuse keys")
	}
	change.Abort()
	if oldQUIC.bindAddress != "127.0.0.1:0" || oldTLS.bindAddress != "127.0.0.1:0" {
		t.Fatal("abort changed live endpoint keys")
	}
	change, err = h.Prepare(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Commit(); err != nil {
		t.Fatal(err)
	}
	if h.quic != oldQUIC || h.tls != oldTLS || oldQUIC.bindAddress != next.RemoteQUICAddress || oldTLS.bindAddress != next.RemoteTLSAddress {
		t.Fatal("commit did not adopt actual addresses without rebinding")
	}
}

func TestCoreRemoteToggleRetainsLiveSSHAndConfiguredListeners(t *testing.T) {
	for _, carrier := range []string{"tls", "quic"} {
		t.Run(carrier, func(t *testing.T) {
			s := remoteHostSettings(t)
			s.RemoteAccessEnabled = true
			h, _ := remoteHostFixture(t, s)
			oldQUIC, oldTLS := h.quic, h.tls
			boot, err := h.access.BootstrapSSH(t.Context(), "live SSH")
			if err != nil {
				t.Fatal(err)
			}
			links := make(chan corewire.Conn, 4)
			client := remoteHostDial(t, func(ctx context.Context) (corewire.Conn, error) {
				var conn corewire.Conn
				var err error
				if carrier == "quic" {
					conn, err = corewire.DialQUIC(ctx, boot.QUICAddress, boot.Credential.Fingerprint)
				} else {
					conn, err = corewire.DialTLS(ctx, boot.TLSAddress, boot.Credential.Fingerprint)
				}
				if err == nil {
					links <- conn
				}
				return conn, err
			}, boot.Credential.Bearer())
			conn := <-links
			owner := client.Owner()
			for _, enabled := range []bool{false, true, false, true} {
				s.RemoteAccessEnabled = enabled
				change, err := h.Prepare(s)
				if err != nil {
					t.Fatal(err)
				}
				if change.newQUIC || change.newTLS {
					t.Fatal("toggle rebound configured listeners")
				}
				if err := change.Commit(); err != nil {
					t.Fatal(err)
				}
				remoteHostStatus(t, client)
				if h.quic != oldQUIC || h.tls != oldTLS || client.Owner() != owner {
					t.Fatal("toggle replaced listener or SSH ownership")
				}
				select {
				case <-conn.Done():
					t.Fatal("toggle terminated established SSH carrier")
				case <-links:
					t.Fatal("toggle forced SSH reconnect")
				default:
				}
			}
		})
	}
}

func TestCoreRemoteFailedAcceptWithdrawsEndpointsAndRetainsError(t *testing.T) {
	s := network.DefaultSettings()
	h, _ := remoteHostFixture(t, s)
	change, err := h.Prepare(s)
	if err != nil {
		t.Fatal(err)
	}
	_ = h.quic.listener.Close()
	_ = h.tls.listener.Close()
	h.workers.Wait() // No clients: both real accept loops have now reported failure.
	if h.Err() == nil {
		t.Fatal("unexpected accept exit was hidden")
	}
	if _, err := h.access.BootstrapSSH(t.Context(), "unavailable"); err == nil {
		t.Fatal("failed accept loop advertised a stale endpoint")
	}
	if err := change.Commit(); err != nil {
		t.Fatal(err)
	}
	if h.Err() == nil {
		t.Fatal("commit of a prepared but failed listener cleared its error")
	}
	if _, err := h.access.BootstrapSSH(t.Context(), "still unavailable"); err == nil {
		t.Fatal("prepared listener failure was republished by commit")
	}
	change, err = h.Prepare(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Commit(); err != nil {
		t.Fatal(err)
	}
	if h.Err() != nil {
		t.Fatal("repair retained old listener error")
	}
	if _, err := h.access.BootstrapSSH(t.Context(), "repaired"); err != nil {
		t.Fatal(err)
	}
}

func TestCoreRemoteDefaultsAndBootstrapAdmission(t *testing.T) {
	config, err := remoteListenerConfig(network.Settings{})
	if err != nil || config.Enabled || config.QUICAddress != "127.0.0.1:7340" || config.TLSAddress != "127.0.0.1:7340" || config.GracePeriod != 15*time.Minute {
		t.Fatalf("default config=%+v err=%v", config, err)
	}
	s := remoteHostSettings(t)
	h, _ := remoteHostFixture(t, s)
	if _, err := h.access.CreatePairing(t.Context()); !errors.Is(err, core.ErrRemoteDisabled) {
		t.Fatalf("disabled pairing: %v", err)
	}
	for _, carrier := range []string{"tls", "quic"} {
		t.Run(carrier, func(t *testing.T) {
			boot, err := h.access.BootstrapSSH(t.Context(), "SSH device")
			if err != nil {
				t.Fatal(err)
			}
			if boot.QUICAddress != h.quic.address || boot.TLSAddress != h.tls.address {
				t.Fatal("bootstrap advertised uncommitted endpoints")
			}
			dial := func(ctx context.Context) (corewire.Conn, error) {
				if carrier == "quic" {
					return corewire.DialQUIC(ctx, boot.QUICAddress, boot.Credential.Fingerprint)
				}
				return corewire.DialTLS(ctx, boot.TLSAddress, boot.Credential.Fingerprint)
			}
			client := remoteHostDial(t, dial, boot.Credential.Bearer())
			remoteHostStatus(t, client)
			if _, err := coreclient.DialWithOptions(t.Context(), dial, core.NewClientID(), coreclient.DialOptions{Credential: boot.Credential.Bearer()}); err == nil {
				t.Fatal("one-use SSH credential replayed")
			}
			// Enable/disable is admission policy, not an SSH session shutdown.
			for _, enabled := range []bool{true, false} {
				s.RemoteAccessEnabled = enabled
				change, err := h.Prepare(s)
				if err != nil {
					t.Fatal(err)
				}
				if err := change.Commit(); err != nil {
					t.Fatal(err)
				}
				remoteHostStatus(t, client)
			}
		})
	}
}

func TestCoreRemotePrepareAbortConflictAndNoop(t *testing.T) {
	s := remoteHostSettings(t)
	s.RemoteAccessEnabled = true
	h, _ := remoteHostFixture(t, s)
	oldQUIC, oldTLS := h.quic, h.tls
	change, err := h.Prepare(s)
	if err != nil {
		t.Fatal(err)
	}
	if change.newQUIC || change.newTLS {
		t.Fatal("unchanged addresses rebound")
	}
	if err := change.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := change.Commit(); err != nil {
		t.Fatal(err)
	}
	change.Abort()
	if h.quic != oldQUIC || h.tls != oldTLS {
		t.Fatal("no-op changed listener identity")
	}
	blocked, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocked.Close() }()
	next := remoteHostSettings(t)
	next.RemoteAccessEnabled = true
	next.RemoteTLSAddress = blocked.Addr().String()
	if _, err := h.Prepare(next); err == nil {
		t.Fatal("conflicting endpoint accepted before persistence")
	}
	if h.Err() == nil {
		t.Fatal("bind failure not observable")
	}
	view, err := h.access.View(t.Context())
	if err != nil || view.Config.QUICAddress != s.RemoteQUICAddress || h.quic != oldQUIC || h.tls != oldTLS {
		t.Fatalf("failed prepare published config: %+v %v", view, err)
	}
	// QUIC reservation made before the TLS bind failed must be released.
	u, err := net.ListenPacket("udp", next.RemoteQUICAddress)
	if err != nil {
		t.Fatalf("failed transaction leaked UDP socket: %v", err)
	}
	_ = u.Close()
	next = remoteHostSettings(t)
	next.RemoteAccessEnabled = true
	change, err = h.Prepare(next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Prepare(s); err == nil {
		t.Fatal("overlapping transaction admitted")
	}
	change.Abort()
	change.Abort()
	if err := change.Commit(); err == nil {
		t.Fatal("aborted transaction committed")
	}
	l, err := net.Listen("tcp", next.RemoteTLSAddress)
	if err != nil {
		t.Fatalf("abort leaked TCP socket: %v", err)
	}
	_ = l.Close()
	u, err = net.ListenPacket("udp", next.RemoteQUICAddress)
	if err != nil {
		t.Fatalf("abort leaked UDP socket: %v", err)
	}
	_ = u.Close()
}

func TestCoreRemoteAddressChangeKeepsClientsAndResumes(t *testing.T) {
	for _, carrier := range []string{"tls", "quic"} {
		t.Run(carrier, func(t *testing.T) {
			s := remoteHostSettings(t)
			s.RemoteAccessEnabled = true
			h, _ := remoteHostFixture(t, s)
			code, err := h.access.CreatePairing(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			address := s.RemoteTLSAddress
			if carrier == "quic" {
				address = s.RemoteQUICAddress
			}
			credential, err := coreclient.PairRemote(t.Context(), address, code.Fingerprint, carrier, core.NewClientID(), coreprotocol.RemotePairRequest{Code: code.Code, DeviceName: "Paired"})
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var latest corewire.Conn
			links := make(chan struct{}, 4)
			dial := func(ctx context.Context) (corewire.Conn, error) {
				mu.Lock()
				target := address
				mu.Unlock()
				var conn corewire.Conn
				var err error
				if carrier == "quic" {
					conn, err = corewire.DialQUIC(ctx, target, code.Fingerprint)
				} else {
					conn, err = corewire.DialTLS(ctx, target, code.Fingerprint)
				}
				if err == nil {
					mu.Lock()
					latest = conn
					mu.Unlock()
					links <- struct{}{}
				}
				return conn, err
			}
			client := remoteHostDial(t, dial, credential.Bearer())
			<-links
			owner := client.Owner()
			next := remoteHostSettings(t)
			next.RemoteAccessEnabled, next.SessionGraceSeconds = true, 60
			change, err := h.Prepare(next)
			if err != nil {
				t.Fatal(err)
			}
			view, err := h.access.View(t.Context())
			if err != nil || view.Config.TLSAddress != s.RemoteTLSAddress {
				t.Fatal("Prepare published before durability")
			}
			mu.Lock()
			original := latest
			address = next.RemoteTLSAddress
			if carrier == "quic" {
				address = next.RemoteQUICAddress
			}
			mu.Unlock()
			if err := change.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-original.Done():
				t.Fatal("address change killed valid established carrier")
			default:
			}
			remoteHostStatus(t, client)
			select {
			case <-links:
				t.Fatal("address change forced a reconnect")
			default:
			}
			_ = original.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			select {
			case <-links:
			case <-ctx.Done():
				t.Fatal("session did not resume on committed endpoint")
			}
			remoteHostStatus(t, client)
			if client.Owner() != owner {
				t.Fatal("endpoint switch replaced session ownership")
			}
			view, err = h.access.View(t.Context())
			if err != nil || view.Config.GracePeriod != time.Minute {
				t.Fatalf("grace=%v err=%v", view.Config.GracePeriod, err)
			}
			next.RemoteAccessEnabled = false
			change, err = h.Prepare(next)
			if err != nil {
				t.Fatal(err)
			}
			if err := change.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := client.Call(ctx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, &coreprotocol.CoreStatus{}); err == nil {
				t.Fatal("disabled paired session retained command access")
			}
		})
	}
}

func TestCoreRemoteAddressReturnPreservesCarriersAndRollback(t *testing.T) {
	a := remoteHostSettings(t)
	a.RemoteAccessEnabled = true
	h, _ := remoteHostFixture(t, a)
	b := remoteHostSettings(t)
	b.RemoteAccessEnabled = true
	type retainedClient struct {
		client *coreclient.Client
		conn   corewire.Conn
		links  <-chan corewire.Conn
		owner  coreprotocol.Owner
	}
	var clients []retainedClient
	connect := func() {
		for _, carrier := range []string{"tls", "quic"} {
			boot, err := h.access.BootstrapSSH(t.Context(), "retained SSH "+carrier)
			if err != nil {
				t.Fatal(err)
			}
			links := make(chan corewire.Conn, 8)
			client := remoteHostDial(t, func(ctx context.Context) (corewire.Conn, error) {
				var conn corewire.Conn
				var err error
				if carrier == "quic" {
					conn, err = corewire.DialQUIC(ctx, boot.QUICAddress, boot.Credential.Fingerprint)
				} else {
					conn, err = corewire.DialTLS(ctx, boot.TLSAddress, boot.Credential.Fingerprint)
				}
				if err == nil {
					links <- conn
				}
				return conn, err
			}, boot.Credential.Bearer())
			clients = append(clients, retainedClient{client, <-links, links, client.Owner()})
		}
	}
	check := func() {
		for _, client := range clients {
			remoteHostStatus(t, client.client)
			if client.client.Owner() != client.owner {
				t.Fatal("return replaced retained session owner")
			}
			select {
			case <-client.conn.Done():
				t.Fatal("return killed an established carrier")
			case <-client.links:
				t.Fatal("return forced carrier reconnection")
			default:
			}
		}
	}
	connect()
	original := h.quic
	change, err := h.Prepare(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Commit(); err != nil {
		t.Fatal(err)
	}
	connect()
	// Failure after reserving the old transport must roll back only acceptance,
	// not the transport or any established carrier on the returned address.
	blocked, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocked.Close() })
	conflict := a
	conflict.RemoteTLSAddress = blocked.Addr().String()
	if _, err := h.Prepare(conflict); err == nil {
		t.Fatal("retained return ignored occupied TLS port")
	}
	check()
	for round := range 3 {
		// Keep actual RPC streams in flight while both listener generations move.
		var finish []func()
		for _, client := range clients {
			finish = append(finish, remoteHostPendingStatus(t, client.conn))
		}
		activeB := h.quic
		change, err := h.Prepare(a)
		if err != nil {
			t.Fatalf("prepare return round %d to %s: %v", round, a.RemoteQUICAddress, err)
		}
		if h.quic != activeB || change.quic == original || change.quic.listener == original.listener {
			t.Fatal("Prepare published or reused mutable accept generation")
		}
		view, err := h.access.View(t.Context())
		if err != nil || view.Config.QUICAddress != b.RemoteQUICAddress {
			t.Fatalf("Prepare published returned endpoint: %+v %v", view, err)
		}
		boot, err := h.access.BootstrapSSH(t.Context(), "prepared return")
		if err != nil || boot.QUICAddress != b.RemoteQUICAddress {
			t.Fatalf("Prepare replaced bootstrap: %+v %v", boot, err)
		}
		// A prepared transport may handshake, but must not consume credentials
		// or run a command before durable Commit. On Abort its queue is closed.
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		_, err = coreclient.DialWithOptions(ctx, func(ctx context.Context) (corewire.Conn, error) {
			return corewire.DialQUIC(ctx, a.RemoteQUICAddress, boot.Credential.Fingerprint)
		}, core.NewClientID(), coreclient.DialOptions{Credential: boot.Credential.Bearer()})
		deadlineErr := ctx.Err()
		cancel()
		if err == nil || !errors.Is(deadlineErr, context.DeadlineExceeded) {
			t.Fatalf("prepared endpoint admitted application traffic: %v", err)
		}
		queued, err := corewire.DialQUIC(t.Context(), a.RemoteQUICAddress, boot.Credential.Fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = queued.Close() })
		change.Abort()
		select {
		case <-queued.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("Abort left unaccepted prepared carrier alive")
		}
		if h.quic != activeB || h.pending != nil {
			t.Fatal("Abort did not preserve B ownership")
		}
		check()
		change, err = h.Prepare(a)
		if err != nil {
			t.Fatal(err)
		}
		if err := change.Commit(); err != nil {
			t.Fatal(err)
		}
		// The timed-out prepared handshake must not have spent its credential.
		fresh := remoteHostDial(t, func(ctx context.Context) (corewire.Conn, error) {
			return corewire.DialQUIC(ctx, a.RemoteQUICAddress, boot.Credential.Fingerprint)
		}, boot.Credential.Bearer())
		remoteHostStatus(t, fresh)
		_ = fresh.Close()
		for _, complete := range finish {
			complete()
		}
		check()
		change, err = h.Prepare(b)
		if err != nil {
			t.Fatal(err)
		}
		if err := change.Commit(); err != nil {
			t.Fatal(err)
		}
		check()
		if len(h.retired) != 1 || h.Err() != nil {
			t.Fatalf("retained resources accumulated or stale worker failed replacement: %d %v", len(h.retired), h.Err())
		}
	}
	// Close while a returned generation is prepared must release both resources,
	// join all carriers and prevent the pending transaction from being committed.
	pending, err := h.Prepare(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pending.Commit(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed prepared return committed: %v", err)
	}
	for _, client := range clients {
		select {
		case <-client.conn.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("Close left a retired carrier alive")
		}
	}
	for _, settings := range []network.Settings{a, b} {
		udp, err := net.ListenPacket("udp", settings.RemoteQUICAddress)
		if err != nil {
			t.Fatalf("Close retained UDP: %v", err)
		}
		_ = udp.Close()
		tcp, err := net.Listen("tcp", settings.RemoteTLSAddress)
		if err != nil {
			t.Fatalf("Close retained TCP: %v", err)
		}
		_ = tcp.Close()
	}
}

// Leave a real framed RPC incomplete across listener retirement/reactivation.
// Finishing it must produce its reply on that same stream and carrier.
func remoteHostPendingStatus(t *testing.T, conn corewire.Conn) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	stream, err := conn.OpenStream(ctx, corewire.ClassInteractive)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stop := context.AfterFunc(ctx, func() { _ = stream.Close() })
	t.Cleanup(func() { stop(); cancel(); _ = stream.Close() })
	m := corewire.NewMessage()
	defer m.Release()
	m.B, err = (&coreprotocol.Open{Kind: coreprotocol.StreamCommand, ID: core.NewClientID(), Method: coreprotocol.CoreStatusMethod}).MarshalMsg(m.B)
	if err != nil {
		t.Fatal(err)
	}
	m.B, err = (&coreprotocol.Empty{}).MarshalMsg(m.B)
	if err != nil {
		t.Fatal(err)
	}
	framed, err := m.Framed()
	if err != nil {
		t.Fatal(err)
	}
	last := framed[len(framed)-1]
	if _, err := stream.Write(framed[:len(framed)-1]); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		defer cancel()
		defer stream.Close()
		if _, err := stream.Write([]byte{last}); err != nil {
			t.Fatal(err)
		}
		reader := corewire.NewReader(stream, corewire.MaxMessage)
		var reply coreprotocol.Reply
		var rest []byte
		for {
			body, err := reader.Next()
			if err != nil {
				t.Fatal(err)
			}
			rest, err = reply.UnmarshalMsg(body)
			if err != nil || reply.Fault != nil {
				t.Fatalf("retained streaming RPC: %+v %v", reply, err)
			}
			if reply.Final {
				break
			}
		}
		var status coreprotocol.CoreStatus
		if rest, err = status.UnmarshalMsg(rest); err != nil || len(rest) != 0 {
			t.Fatalf("retained streaming RPC result: %v trailing=%d", err, len(rest))
		}
		ack, err := coreprotocol.Marshal(&coreprotocol.TopicAck{Seq: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := corewire.WriteMessage(stream, ack); err != nil {
			t.Fatal(err)
		}
		_ = stream.CloseWrite()
		_, _ = reader.Next()
	}
}

func TestCoreRemoteFailedStartupKeepsLocalCoreAndCanRepair(t *testing.T) {
	r := remoteHostRuntime(t)
	s := remoteHostSettings(t)
	s.RemoteAccessEnabled = true
	blocked, err := net.Listen("tcp", s.RemoteTLSAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocked.Close() }()
	h, err := newCoreRemote(t.Context(), r, filepath.Join(t.TempDir(), "remote"), s, nil)
	if err == nil || h == nil {
		t.Fatalf("host=%v err=%v", h, err)
	}
	t.Cleanup(func() { _ = h.Close() })
	if _, err := h.access.BootstrapSSH(t.Context(), "SSH"); err == nil {
		t.Fatal("failed startup advertised unavailable endpoint")
	}
	server, peer := net.Pipe()
	done := make(chan struct{})
	go func() { defer close(done); _ = core.ServeConnection(t.Context(), r, server) }()
	client := remoteHostDial(t, func(context.Context) (corewire.Conn, error) {
		return corewire.NewMux(peer, corewire.CarrierLocal, true), nil
	}, "")
	remoteHostStatus(t, client)
	change, err := h.Prepare(remoteHostSettings(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Commit(); err != nil {
		t.Fatal(err)
	}
	if h.Err() != nil {
		t.Fatal("successful repair kept stale error")
	}
	remoteHostStatus(t, client)
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("local server did not stop")
	}
}

func TestCoreRemoteCloseJoinsRetiredAndPreparedResources(t *testing.T) {
	s := remoteHostSettings(t)
	s.RemoteAccessEnabled = true
	h, _ := remoteHostFixture(t, s)
	boot, err := h.access.BootstrapSSH(t.Context(), "SSH")
	if err != nil {
		t.Fatal(err)
	}
	carriers := make(chan corewire.Conn, 1)
	client := remoteHostDial(t, func(ctx context.Context) (corewire.Conn, error) {
		conn, err := corewire.DialTLS(ctx, boot.TLSAddress, boot.Credential.Fingerprint)
		if err == nil {
			select {
			case carriers <- conn:
			default:
			}
		}
		return conn, err
	}, boot.Credential.Bearer())
	conn := <-carriers // Assert the accepted carrier, never a racing replacement.
	remoteHostStatus(t, client)
	next := remoteHostSettings(t)
	next.RemoteAccessEnabled = true
	change, err := h.Prepare(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Commit(); err != nil {
		t.Fatal(err)
	}
	uncommitted := remoteHostSettings(t)
	uncommitted.RemoteAccessEnabled = true
	pending, err := h.Prepare(uncommitted)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { _ = h.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close failed to join live retired carrier")
	}
	if _, err := h.access.BootstrapSSH(t.Context(), "closed"); err == nil {
		t.Fatal("closed host advertised endpoints")
	}
	select {
	case <-conn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Close left accepted carrier alive")
	}
	if err := pending.Commit(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("pending commit after Close: %v", err)
	}
	if _, err := h.Prepare(s); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("prepare after Close: %v", err)
	}
	l, err := net.Listen("tcp", uncommitted.RemoteTLSAddress)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	u, err := net.ListenPacket("udp", uncommitted.RemoteQUICAddress)
	if err != nil {
		t.Fatal(err)
	}
	_ = u.Close()
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
}
