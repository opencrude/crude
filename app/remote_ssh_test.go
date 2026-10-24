package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/core"
	"crude/coreprotocol"
	"crude/corewire"
)

// Re-execute the test image as the fixed remote stdio command so this journey
// crosses owned process pipes, SSH compression, the real local socket and both
// authenticated direct carriers. It does not require an external SSH account.
func TestRemoteSSHRelayProcess(t *testing.T) {
	if os.Getenv("CRUDE_TEST_SSH_RELAY") != "1" {
		return
	}
	err := RunCoreStdio(os.Getenv("CRUDE_TEST_SSH_HOME"))
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func TestRemoteSSHDialerUpgradeAndResumeFallback(t *testing.T) {
	o := remoteInstallFixture(t)
	image, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "crude"), []byte("#!/bin/sh\nexec \"$CRUDE_TEST_SSH_IMAGE\" -test.run '^TestRemoteSSHRelayProcess$'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CRUDE_TEST_SSH_IMAGE", image)
	t.Setenv("CRUDE_TEST_SSH_RELAY", "1")
	// Unix socket paths have a platform length bound; the profile is isolated.
	home, err := os.MkdirTemp("", "crude-ssh-journey-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("CRUDE_HOME", home)
	t.Setenv("CRUDE_TEST_SSH_HOME", home)
	if err := os.MkdirAll(DataDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	h, r := remoteHostFixture(t, remoteHostSettings(t))
	// A native peer's image hash may differ (for example across platforms) while
	// the source cohort is identical. Image status must not reject that session.
	r.SetBuildID("different-native-executable")
	ctx, cancel := context.WithCancel(t.Context())
	l, err := corewire.ListenLocal(ctx, DataDir(home))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- core.Serve(ctx, r, l) }()
	t.Cleanup(func() { cancel(); _ = l.Close(); <-served })
	dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
	defer dialCancel()
	client, err := remoteSSHDialer(home, o)(dialCtx, "ssh-upgrade-journey")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	owner := client.Owner()
	carrier, _ := client.Carrier()
	if carrier != corewire.CarrierQUIC {
		t.Fatalf("SSH did not upgrade: %s", carrier)
	}
	remoteHostStatus(t, client)
	var status coreprotocol.CoreStatus
	if err := client.Call(dialCtx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, &status); err != nil || status.BuildID != "different-native-executable" {
		t.Fatalf("native image status must not be protocol identity: %+v %v", status, err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	remoteHostStatus(t, client)
	carrier, _ = client.Carrier()
	if carrier != corewire.CarrierSSH || client.Owner() != owner {
		t.Fatalf("resume lost owner or fallback: carrier=%s owner=%+v want=%+v", carrier, client.Owner(), owner)
	}
	if _, err := os.Stat(filepath.Join(DataDir(home), "remote-bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("matching PATH peer triggered installation")
	}
}

// The raw test-only peer emits a current typed rejection, not a production build
// override. The malformed variant proves exact tuple decoding has no fallback.
func TestRemoteSSHHandshakeProcess(t *testing.T) {
	mode := os.Getenv("CRUDE_TEST_SSH_HANDSHAKE")
	if mode == "" {
		return
	}
	conn := corewire.NewMux(stdioRelay{}, corewire.CarrierSSH, false)
	stream, err := conn.AcceptStream(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	body, err := corewire.NewReader(stream, 64<<10).Next()
	if err != nil {
		t.Fatal(err)
	}
	var hello coreprotocol.Hello
	if err := coreprotocol.Decode(body, &hello); err != nil || hello.Build != buildinfo.ProtocolBuild || hello.Schema != coreprotocol.Schema {
		t.Fatalf("SSH did not send exact cohort/schema: %+v %v", hello, err)
	}
	welcome := coreprotocol.Welcome{Schema: coreprotocol.Schema, Build: strings.Repeat("0", 64), Fault: &coreprotocol.Fault{Code: "build_mismatch", Message: "install the matching source cohort"}}
	body, err = welcome.MarshalMsg(nil)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "malformed" {
		body = append(body, 0xc0) // A valid Welcome plus trailing data is invalid.
	}
	if err := corewire.WriteMessage(stream, body); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, stream)
	_ = conn.Close()
	os.Exit(0)
}

func TestRemoteSSHDialerTypedCohortMismatchAndMalformedWelcome(t *testing.T) {
	for _, mode := range []string{"mismatch", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			o := remoteInstallFixture(t)
			image, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "crude"), []byte("#!/bin/sh\nexec \"$CRUDE_TEST_SSH_IMAGE\" -test.run '^TestRemoteSSHHandshakeProcess$'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("CRUDE_TEST_SSH_IMAGE", image)
			t.Setenv("CRUDE_TEST_SSH_HANDSHAKE", mode)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			client, err := remoteSSHDialer("", o)(ctx, "ssh-raw-handshake")
			if client != nil || err == nil {
				if client != nil {
					_ = client.Close()
				}
				t.Fatal("invalid cohort handshake admitted")
			}
			var offer *RemoteInstallRequiredError
			if mode == "mismatch" {
				var fault *coreprotocol.Fault
				if !errors.As(err, &offer) || !errors.As(err, &fault) || fault.Code != "build_mismatch" {
					t.Fatalf("typed cohort mismatch did not offer reviewed install: %v", err)
				}
			} else if errors.As(err, &offer) || !errors.Is(err, coreprotocol.ErrTrailingBytes) {
				t.Fatalf("malformed Welcome was not rejected by exact tuple decoding: %v", err)
			}
			if _, err := os.Stat(filepath.Join(os.Getenv("CRUDE_HOME"), "."+buildinfo.DataDirName, "remote-bin")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("handshake failure installed without consent")
			}
		})
	}
}

func TestSSHUpgradeAddressesAndTrust(t *testing.T) {
	for _, test := range []struct{ in, out string }{
		{"0.0.0.0:4444", "alias:4444"}, {"[::]:5555", "alias:5555"}, {"127.0.0.1:4444", "127.0.0.1:4444"}, {"", ""},
	} {
		got, err := sshDirectAddress(test.in, "alias")
		if err != nil || got != test.out {
			t.Fatalf("%q => %q %v", test.in, got, err)
		}
	}
	for _, address := range []string{"host", "host:0", "host:99999"} {
		if _, err := sshDirectAddress(address, "alias"); err == nil {
			t.Fatal("invalid address accepted", address)
		}
	}
	if _, err := sshUpgradeDialer("alias", coreprotocol.RemoteSSHBootstrapResult{}, nil); err == nil {
		t.Fatal("missing trusted pin accepted")
	}
}

func TestSSHUpgradeBlockedUDPUsesPinnedTLSAndSSHOnLoss(t *testing.T) {
	identity, err := corewire.LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, err := corewire.ListenTLS("127.0.0.1:0", identity)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	fallbacks := 0
	fallback := func(context.Context) (corewire.Conn, error) {
		fallbacks++
		return nil, errors.New("verified SSH fallback")
	}
	bootstrap := coreprotocol.RemoteSSHBootstrapResult{Credential: coreprotocol.RemoteCredential{Fingerprint: identity.Fingerprint}, QUICAddress: "127.0.0.1:1", TLSAddress: l.Addr().String()}
	dial, err := sshUpgradeDialer("alias", bootstrap, fallback)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, err := dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if conn.Carrier() != corewire.CarrierTCP || fallbacks != 0 {
		t.Fatalf("carrier=%s fallback=%d", conn.Carrier(), fallbacks)
	}
	_ = conn.Close()
	_ = l.Close()
	if _, err := dial(ctx); err == nil || !strings.Contains(err.Error(), "verified SSH fallback") || fallbacks != 1 {
		t.Fatalf("lost direct endpoint fallback=%d err=%v", fallbacks, err)
	}
}

func TestSSHUpgradePinMismatchNeverAdmitsDirect(t *testing.T) {
	identity, err := corewire.LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, err := corewire.ListenTLS("127.0.0.1:0", identity)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	calls := 0
	dial, err := sshUpgradeDialer("alias", coreprotocol.RemoteSSHBootstrapResult{Credential: coreprotocol.RemoteCredential{Fingerprint: strings.Repeat("a", 64)}, TLSAddress: l.Addr().String()}, func(context.Context) (corewire.Conn, error) { calls++; return nil, errors.New("SSH") })
	if err != nil {
		t.Fatal(err)
	}
	if conn, err := dial(t.Context()); err == nil || conn != nil || calls != 1 {
		t.Fatal("pin mismatch admitted", err)
	}
}
