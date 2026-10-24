package core

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/types"
	"crude/workspace"
)

type remoteJourney struct {
	r                   *Runtime
	access              *RemoteAccess
	localDir, tls, quic string
	clock               atomic.Int64
	created             atomic.Int32
}

func remoteJourneyFixture(t *testing.T) *remoteJourney {
	t.Helper()
	f := &remoteJourney{}
	f.clock.Store(time.Now().UnixNano())
	r, err := New(func(root string) (*workspace.Workspace, error) {
		f.created.Add(1)
		return workspace.NewWorkspace(root, "", "remote-test", types.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	f.r = r
	t.Cleanup(func() { _ = r.Close() })
	if r.buildID() != buildinfo.ExecutableID() {
		t.Fatal("runtime default build identity differs from the running SDK image")
	}
	access, err := OpenRemoteAccess(remotePrivateTestDir(t), RemoteAccessOptions{Config: remoteTestConfig(), Now: func() time.Time { return time.Unix(0, f.clock.Load()) }})
	if err != nil {
		t.Fatal(err)
	}
	f.access = access
	if err := r.SetRemoteAccess(access); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	// Use the actual ownership-checked local endpoint, not a network socket
	// relabelled as local. A short path is required by sockaddr_un.
	dir, err := os.MkdirTemp("", "crude-remote-")
	if err != nil {
		t.Fatal(err)
	}
	f.localDir = dir
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	l, err := corewire.ListenLocal(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	tls, err := corewire.ListenTLS("127.0.0.1:0", access.Identity())
	if err != nil {
		t.Fatal(err)
	}
	quic, err := corewire.ListenQUIC("127.0.0.1:0", access.Identity())
	if err != nil {
		t.Fatal(err)
	}
	f.tls, f.quic = tls.Addr().String(), quic.Addr().String()
	cfg := remoteTestConfig()
	if err := r.ConfigureRemoteAccess(cfg, SSHUpgradeEndpoints{TLSAddress: f.tls, QUICAddress: f.quic}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, serve := range []func() error{
		func() error { return Serve(ctx, r, l) },
		func() error { return ServeTLS(ctx, r, tls, r.RemoteAuthenticator()) },
		func() error { return ServeQUIC(ctx, r, quic, r.RemoteAuthenticator()) },
	} {
		wg.Add(1)
		go func() { defer wg.Done(); _ = serve() }()
	}
	t.Cleanup(func() { cancel(); _ = l.Close(); _ = tls.Close(); _ = quic.Close(); wg.Wait() })
	return f
}

func (f *remoteJourney) localDial(ctx context.Context) (corewire.Conn, error) {
	c, err := corewire.DialLocal(ctx, f.localDir)
	if err != nil {
		return nil, err
	}
	return corewire.NewMux(c, corewire.CarrierLocal, true), nil
}
func (f *remoteJourney) dial(t *testing.T, carrier string) coreclient.Dialer {
	t.Helper()
	return func(ctx context.Context) (corewire.Conn, error) {
		if carrier == "quic" {
			return corewire.DialQUIC(ctx, f.quic, f.access.Identity().Fingerprint)
		}
		return corewire.DialTLS(ctx, f.tls, f.access.Identity().Fingerprint)
	}
}
func remoteClient(t *testing.T, dial coreclient.Dialer, id, credential string) *coreclient.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	c, err := coreclient.DialWithOptions(ctx, dial, id, coreclient.DialOptions{Credential: credential})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
func remoteFault(t *testing.T, err error, code string) {
	t.Helper()
	var f *coreprotocol.Fault
	if !errors.As(err, &f) || f == nil || f.Code != code {
		t.Fatalf("want fault %s, got %v", code, err)
	}
}
func remoteHello(t *testing.T, conn corewire.Conn, hello coreprotocol.Hello) (coreprotocol.Welcome, corewire.Stream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	s, err := conn.OpenStream(ctx, corewire.ClassControl)
	if err != nil {
		t.Fatal(err)
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	encoded, err := coreprotocol.Marshal(&hello)
	if err != nil {
		t.Fatal(err)
	}
	if err := corewire.WriteMessage(s, encoded); err != nil {
		t.Fatal(err)
	}
	body, err := corewire.NewReader(s, 64<<10).Next()
	if err != nil {
		t.Fatal(err)
	}
	var welcome coreprotocol.Welcome
	if err := coreprotocol.Decode(body, &welcome); err != nil {
		t.Fatal(err)
	}
	if welcome.Fault != nil {
		_ = s.CloseWrite()
	}
	return welcome, s
}

func TestRemoteTransportPairResumeRevokeJourney(t *testing.T) {
	for _, carrier := range []string{"tls", "quic"} {
		t.Run(carrier, func(t *testing.T) {
			f := remoteJourneyFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			admin := remoteClient(t, f.localDial, "host-admin", "")
			if _, err := admin.BootstrapRemoteSSH(ctx, "not SSH"); err == nil {
				t.Fatal("local admin minted SSH-only bootstrap")
			} else {
				remoteFault(t, err, "forbidden")
			}
			code, err := admin.CreateRemotePairing(ctx)
			if err != nil {
				t.Fatal(err)
			}
			address := f.tls
			if carrier == "quic" {
				address = f.quic
			}
			if _, err := coreclient.PairRemote(ctx, address, strings.Repeat("0", 64), carrier, "bad-pin", coreprotocol.RemotePairRequest{Code: code.Code, DeviceName: "Bad"}); err == nil {
				t.Fatal("wrong pin paired")
			}
			if _, err := coreclient.PairRemote(ctx, address, code.Fingerprint, carrier, "wrong-code", coreprotocol.RemotePairRequest{Code: "wrong", DeviceName: "Bad"}); err == nil {
				t.Fatal("wrong code paired")
			}
			credential, err := coreclient.PairRemote(ctx, address, code.Fingerprint, carrier, "pair-only", coreprotocol.RemotePairRequest{Code: code.Code, DeviceName: "Laptop"})
			if err != nil {
				t.Fatal(err)
			}
			if f.r.ClientCount() != 1 || f.created.Load() != 0 {
				t.Fatal("pairing admitted a session or workspace")
			}
			view, err := admin.RemoteAccess(ctx)
			if err != nil || len(view.Devices) != 1 || view.Devices[0].ID != credential.DeviceID || !view.Devices[0].LastSeen.IsZero() {
				t.Fatalf("host device view after enrollment: %+v %v", view, err)
			}
			if _, err := coreclient.PairRemote(ctx, address, code.Fingerprint, carrier, "replay", coreprotocol.RemotePairRequest{Code: code.Code, DeviceName: "Replay"}); err == nil {
				t.Fatal("code replay enrolled second device")
			}
			expired, err := admin.CreateRemotePairing(ctx)
			if err != nil {
				t.Fatal(err)
			}
			f.clock.Store(expired.ExpiresAt.UnixNano())
			if _, err := coreclient.PairRemote(ctx, address, code.Fingerprint, carrier, "expired", coreprotocol.RemotePairRequest{Code: expired.Code, DeviceName: "Expired"}); err == nil {
				t.Fatal("expired code paired")
			}
			dial := f.dial(t, carrier)
			var latestMu sync.Mutex
			var latest corewire.Conn
			links := make(chan struct{}, 4)
			tracked := func(ctx context.Context) (corewire.Conn, error) {
				c, e := dial(ctx)
				if e == nil {
					latestMu.Lock()
					latest = c
					latestMu.Unlock()
					links <- struct{}{}
				}
				return c, e
			}
			client := remoteClient(t, tracked, "paired-client", credential.Bearer())
			<-links
			root := t.TempDir()
			var attached coreprotocol.AttachResult
			if err := client.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
				t.Fatal(err)
			}
			owner := client.Owner()
			for _, operation := range []func() error{
				func() error { _, e := client.RemoteAccess(ctx); return e },
				func() error { _, e := client.CreateRemotePairing(ctx); return e },
				func() error { return client.RevokeRemoteDevice(ctx, credential.DeviceID) },
				func() error { _, e := client.BootstrapRemoteSSH(ctx, "spoof"); return e },
			} {
				remoteFault(t, operation(), "forbidden")
			}
			latestMu.Lock()
			lost := latest
			latestMu.Unlock()
			_ = lost.Close()
			select {
			case <-links:
			case <-ctx.Done():
				t.Fatal("client did not replace carrier")
			}
			var snapshot coreprotocol.WorkspaceSnapshot
			if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.Snapshot, &coreprotocol.Empty{}, &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.Root != root || client.Owner() != owner || f.created.Load() != 1 {
				t.Fatal("resume replaced principal, owner, or attachment")
			}
			remoteFault(t, func() error { _, e := client.RemoteAccess(ctx); return e }(), "forbidden")
			// Retain a second device session without a carrier. Revocation must
			// invalidate its independent resume secret as well as the active link.
			detached, err := dial(ctx)
			if err != nil {
				t.Fatal(err)
			}
			retained, _ := remoteHello(t, detached, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: "revoked-resume", Credential: credential.Bearer()})
			if retained.Fault != nil {
				t.Fatal(retained.Fault)
			}
			_ = detached.Close()
			if err := admin.RevokeRemoteDevice(ctx, credential.DeviceID); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.Done():
			case <-ctx.Done():
				t.Fatal("revocation did not end active client")
			}
			if _, err := coreclient.DialWithOptions(ctx, dial, "revoked-new", coreclient.DialOptions{Credential: credential.Bearer()}); err == nil {
				t.Fatal("revoked credential reconnected")
			} else {
				remoteFault(t, err, "unauthorized")
			}
			retry, err := dial(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer retry.Close()
			refused, _ := remoteHello(t, retry, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: "revoked-resume", Resume: &coreprotocol.SessionResume{SessionID: retained.SessionID, Secret: retained.Secret}})
			remoteFault(t, refused.Fault, "session_gone")
		})
	}
}

func TestRemoteTransportRejectsNetworkAuthorityClaims(t *testing.T) {
	f := remoteJourneyFixture(t)
	ctx := t.Context()
	adminConn, err := f.localDial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	welcome, _ := remoteHello(t, adminConn, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: "local-resume"})
	if welcome.Fault != nil {
		t.Fatal(welcome.Fault)
	}
	defer adminConn.Close()
	network, err := f.dial(t, "tls")(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer network.Close()
	resumed, _ := remoteHello(t, network, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: "local-resume", Resume: &coreprotocol.SessionResume{SessionID: welcome.SessionID, Secret: welcome.Secret}})
	remoteFault(t, resumed.Fault, "unauthorized")
	network, err = f.dial(t, "tls")(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer network.Close()
	spoof, _ := remoteHello(t, network, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: "ssh-spoof", SSHRelay: true})
	remoteFault(t, spoof.Fault, "unauthorized")
	// A custom authenticator must not turn an empty/default principal into
	// authenticated network authority, even when it forgets to return an error.
	a, b := net.Pipe()
	server := corewire.NewMux(b, corewire.CarrierTCP, false)
	done := make(chan error, 1)
	go func() {
		done <- ServeCarrier(ctx, f.r, server, func(context.Context, coreprotocol.Hello) (RemotePrincipal, error) { return RemotePrincipal{}, nil })
	}()
	c := corewire.NewMux(a, corewire.CarrierTCP, true)
	defer c.Close()
	empty, _ := remoteHello(t, c, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: "empty-principal"})
	remoteFault(t, empty.Fault, "unauthorized")
	_ = c.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refused custom-auth carrier did not join")
	}
}

func TestRemoteTransportPairBuildMismatchIsExplicit(t *testing.T) {
	f := remoteJourneyFixture(t)
	code, err := f.access.CreatePairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := f.dial(t, "tls")(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	welcome, _ := remoteHello(t, conn, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: strings.Repeat("0", 64), ClientID: "mismatched-build", Pair: &coreprotocol.RemotePairRequest{Code: code.Code, DeviceName: "Laptop"}})
	remoteFault(t, welcome.Fault, "build_mismatch")
	if f.r.ClientCount() != 0 {
		t.Fatal("build mismatch created session")
	}
	// Refused admission must not consume the one-use enrollment code.
	if _, err := coreclient.PairRemote(t.Context(), f.tls, code.Fingerprint, "tls", "matching-build", coreprotocol.RemotePairRequest{Code: code.Code, DeviceName: "Laptop"}); err != nil {
		t.Fatal("mismatch consumed pairing code", err)
	}
}

func TestRemoteSSHRelayBootstrapUpgradeAndCompressedRPC(t *testing.T) {
	f := remoteJourneyFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	local, err := f.localDial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	incoming := corewire.NewMux(b, corewire.CarrierSSH, false)
	ssh := corewire.NewMux(a, corewire.CarrierSSH, true)
	relayDone := make(chan error, 1)
	go func() { relayDone <- coreclient.RelaySSH(ctx, incoming, local) }()
	client := remoteClient(t, func(context.Context) (corewire.Conn, error) { return ssh, nil }, "verified-ssh", "")
	if _, err := client.CreateRemotePairing(ctx); err == nil {
		t.Fatal("SSH became local admin")
	} else {
		remoteFault(t, err, "forbidden")
	}
	bootstrap, err := client.BootstrapRemoteSSH(ctx, "SSH laptop")
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.QUICAddress != f.quic || bootstrap.Credential.Fingerprint != f.access.Identity().Fingerprint {
		t.Fatal("bootstrap lost pinned endpoint")
	}
	// A valid large typed request and response traverse independent compressed
	// SSH and uncompressed local mux links, not raw bytes copied between muxes.
	root := t.TempDir()
	var attached coreprotocol.AttachResult
	if err := client.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	var large coreprotocol.StartupSnapshot
	path := filepath.Join(root, strings.Repeat("x", 12<<10))
	if err := client.Call(ctx, "", coreprotocol.StartupInspect, &coreprotocol.StartupInspectRequest{Root: path}, &large); err != nil {
		t.Fatal(err)
	}
	if large.Root != path || large.PathError == "" {
		t.Fatal("large compressed RPC changed content or local error status")
	}
	var status coreprotocol.CoreStatus
	if err := client.Call(ctx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, &status); err != nil {
		t.Fatal(err)
	}
	// Administrative status intentionally excludes the querying session.
	if status.BuildID != buildinfo.ExecutableID() || status.Clients != 0 || len(status.Workspaces) != 1 || status.Workspaces[0].ID != attached.WorkspaceID {
		t.Fatalf("local core status lost through relay: %+v; expected build %s", status, buildinfo.ExecutableID())
	}
	upgraded := remoteClient(t, f.dial(t, "quic"), "ssh-upgrade", bootstrap.Credential.Bearer())
	if _, err := upgraded.CreateRemotePairing(ctx); err == nil {
		t.Fatal("bootstrap upgraded to local admin")
	} else {
		remoteFault(t, err, "forbidden")
	}
	if _, err := coreclient.DialWithOptions(ctx, f.dial(t, "tls"), "bootstrap-replay", coreclient.DialOptions{Credential: bootstrap.Credential.Bearer()}); err == nil {
		t.Fatal("one-use SSH bootstrap reused")
	} else {
		remoteFault(t, err, "unauthorized")
	}
	// A raw bootstrap session proves its independent resume secret survives
	// QUIC -> TLS without presenting the spent bootstrap credential again.
	second, err := client.BootstrapRemoteSSH(ctx, "second upgrade")
	if err != nil {
		t.Fatal(err)
	}
	q, err := f.dial(t, "quic")(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := remoteHello(t, q, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: "bootstrap-resume", Credential: second.Credential.Bearer()})
	if w.Fault != nil || w.Secret == "" {
		t.Fatal("upgrade failed", w.Fault)
	}
	_ = q.Close()
	tcp, err := f.dial(t, "tls")(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	resumed, control := remoteHello(t, tcp, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: "bootstrap-resume", Resume: &coreprotocol.SessionResume{SessionID: w.SessionID, Secret: w.Secret}})
	if resumed.Fault != nil || !resumed.Resumed || resumed.Owner != w.Owner {
		t.Fatal("bootstrap resume failed", resumed.Fault)
	}
	_ = control.CloseWrite()
	cancel()
	select {
	case err := <-relayDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("relay cancellation failed to join active streams")
	}
}
