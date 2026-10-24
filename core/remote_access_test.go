package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/coreprotocol"
)

func remotePrivateTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func remoteTestConfig() coreprotocol.RemoteAccessConfig {
	return coreprotocol.RemoteAccessConfig{Enabled: true, QUICAddress: "127.0.0.1:7340", TLSAddress: "127.0.0.1:7340"}
}
func TestRemoteAccessPairAuthenticateRevokeDurable(t *testing.T) {
	dir := remotePrivateTestDir(t)
	now := time.Unix(1800000000, 0)
	events := []RemoteAuditEvent{}
	s, err := OpenRemoteAccess(dir, RemoteAccessOptions{Config: remoteTestConfig(), Now: func() time.Time { return now }, Audit: func(e RemoteAuditEvent) { events = append(events, e) }})
	if err != nil {
		t.Fatal(err)
	}
	if s.GracePeriod() != 15*time.Minute {
		t.Fatal("wrong default grace")
	}
	code, err := s.CreatePairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.Pair(t.Context(), coreprotocol.RemotePairRequest{Code: code.Code, DeviceName: "Laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pair(t.Context(), coreprotocol.RemotePairRequest{Code: code.Code, DeviceName: "Replay"}); !errors.Is(err, ErrRemoteCredential) {
		t.Fatalf("pair code reused: %v", err)
	}
	for _, bad := range []string{"", credential.Token[:len(credential.Token)-1], "x" + credential.Token[1:], credential.Token + "x"} {
		if _, err = s.Authenticate(t.Context(), credential.DeviceID, bad); !errors.Is(err, ErrRemoteCredential) {
			t.Fatalf("bad token accepted: %v", err)
		}
	}
	if _, err = s.Authenticate(t.Context(), "unknown", credential.Token); !errors.Is(err, ErrRemoteCredential) {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	principal, err := s.AuthenticateCredential(t.Context(), credential.Bearer())
	if err != nil || principal.DeviceID != credential.DeviceID {
		t.Fatalf("auth %+v %v", principal, err)
	}
	v, err := s.View(t.Context())
	if err != nil || len(v.Devices) != 1 || !v.Devices[0].LastSeen.Equal(now) {
		t.Fatalf("view %+v %v", v, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "devices.json"))
	public, _ := json.Marshal(v)
	for _, text := range []string{string(raw), string(public), fmt.Sprintf("%+v %#v", credential, code)} {
		if strings.Contains(text, credential.Token) || strings.Contains(text, code.Code) {
			t.Fatal("raw secret leaked")
		}
	}
	if strings.Contains(string(public), "hash") {
		t.Fatal("public view leaked digest")
	}
	if len(events) != 2 || events[0].Kind != "device_paired" || events[1].Kind != "pairing_rejected" {
		t.Fatalf("audit %+v", events)
	}
	reopened, err := OpenRemoteAccess(dir, RemoteAccessOptions{Config: remoteTestConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Identity().Fingerprint != s.Identity().Fingerprint {
		t.Fatal("identity changed on reopen")
	}
	if _, err = reopened.AuthenticateCredential(t.Context(), credential.Bearer()); err != nil {
		t.Fatal(err)
	}
	if err = reopened.Revoke(t.Context(), credential.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateCredential(t.Context(), credential.Bearer()); !errors.Is(err, ErrRemoteCredential) {
		t.Fatalf("revoked auth: %v", err)
	}
	if err = s.CheckDevice(t.Context(), credential.DeviceID); !errors.Is(err, ErrRemoteCredential) {
		t.Fatalf("live device check: %v", err)
	}
}
func TestRemotePairingExpiryRateLimitAndReplacement(t *testing.T) {
	now := time.Unix(1800000000, 0)
	s, err := OpenRemoteAccess(remotePrivateTestDir(t), RemoteAccessOptions{Config: remoteTestConfig(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := s.CreatePairing(t.Context())
	newCode, _ := s.CreatePairing(t.Context())
	if _, err = s.Pair(t.Context(), coreprotocol.RemotePairRequest{Code: old.Code, DeviceName: "old"}); !errors.Is(err, ErrRemoteCredential) {
		t.Fatal(err)
	}
	if _, err = s.Pair(t.Context(), coreprotocol.RemotePairRequest{Code: newCode.Code, DeviceName: "new"}); err != nil {
		t.Fatal(err)
	}
	expired, _ := s.CreatePairing(t.Context())
	now = expired.ExpiresAt
	if _, err = s.Pair(t.Context(), coreprotocol.RemotePairRequest{Code: expired.Code, DeviceName: "expired"}); !errors.Is(err, ErrRemoteCredential) {
		t.Fatal(err)
	}
	good, _ := s.CreatePairing(t.Context())
	now = now.Add(time.Minute)
	for range 5 {
		if _, err = s.Pair(t.Context(), coreprotocol.RemotePairRequest{Code: "bad", DeviceName: "attacker"}); !errors.Is(err, ErrRemoteCredential) {
			t.Fatal(err)
		}
	}
	if _, err = s.Pair(t.Context(), coreprotocol.RemotePairRequest{Code: good.Code, DeviceName: "limited"}); !errors.Is(err, ErrPairingLimited) {
		t.Fatalf("missing rate limit: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err = s.Pair(t.Context(), coreprotocol.RemotePairRequest{Code: good.Code, DeviceName: "exhausted"}); !errors.Is(err, ErrRemoteCredential) {
		t.Fatalf("exhausted code revived: %v", err)
	}
	fresh, _ := s.CreatePairing(t.Context())
	if _, err = s.Pair(t.Context(), coreprotocol.RemotePairRequest{Code: fresh.Code, DeviceName: "recovered"}); err != nil {
		t.Fatal(err)
	}
}
func TestRemoteBootstrapOneUseConcurrentAndOptIn(t *testing.T) {
	now := time.Unix(1800000000, 0)
	cfg := remoteTestConfig()
	cfg.Enabled = false
	s, err := OpenRemoteAccess(remotePrivateTestDir(t), RemoteAccessOptions{Config: cfg, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreatePairing(t.Context()); !errors.Is(err, ErrRemoteDisabled) {
		t.Fatal(err)
	}
	if _, err = s.BootstrapSSH(t.Context(), "not yet listening"); err == nil {
		t.Fatal("initial requested config advertised an unbound endpoint")
	}
	endpoints := SSHUpgradeEndpoints{QUICAddress: "127.0.0.1:17340", TLSAddress: "127.0.0.1:17341"}
	if err = s.Configure(cfg, endpoints); err != nil {
		t.Fatal(err)
	}
	b, err := s.BootstrapSSH(t.Context(), "SSH device")
	if err != nil {
		t.Fatal(err)
	}
	if b.QUICAddress != endpoints.QUICAddress || b.TLSAddress != endpoints.TLSAddress || b.Credential.Fingerprint == "" {
		t.Fatal("bootstrap lacks pinned endpoint")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := s.AuthenticateCredential(t.Context(), b.Credential.Bearer())
			if e == nil {
				if !p.SSHBootstrap {
					t.Error("lost SSH authority")
				}
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("one-use accepted %d times", success)
	}
	b, _ = s.BootstrapSSH(t.Context(), "expiring")
	now = b.ExpiresAt
	if _, err = s.AuthenticateCredential(t.Context(), b.Credential.Bearer()); !errors.Is(err, ErrRemoteCredential) {
		t.Fatal(err)
	}
	cfg.Enabled = true
	cfg.GracePeriod = time.Hour
	if err = s.Configure(cfg, endpoints); err != nil || s.GracePeriod() != time.Hour {
		t.Fatal(err)
	}
	code, _ := s.CreatePairing(t.Context())
	credential, err := s.Pair(t.Context(), coreprotocol.RemotePairRequest{Code: code.Code, DeviceName: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Enabled = false
	if err = s.Configure(cfg, endpoints); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateCredential(t.Context(), credential.Bearer()); !errors.Is(err, ErrRemoteCredential) {
		t.Fatal("disabled direct access still authenticated")
	}
}
func TestRemoteConfigValidation(t *testing.T) {
	for _, c := range []coreprotocol.RemoteAccessConfig{{Enabled: true}, {GracePeriod: -1}, {GracePeriod: 25 * time.Hour}, {QUICAddress: "bad"}, {TLSAddress: "host:99999"}} {
		if ValidateRemoteAccessConfig(c) == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}

func TestRemoteSSHLiveEndpointsAndConditionalWithdrawal(t *testing.T) {
	cfg := remoteTestConfig()
	s, err := OpenRemoteAccess(remotePrivateTestDir(t), RemoteAccessOptions{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BootstrapSSH(t.Context(), "no listener"); err == nil {
		t.Fatal("enabled initial config guessed a live listener")
	}
	old := SSHUpgradeEndpoints{QUICAddress: "127.0.0.1:17340", TLSAddress: "127.0.0.1:17341"}
	next := SSHUpgradeEndpoints{QUICAddress: "127.0.0.1:17342", TLSAddress: "127.0.0.1:17343"}
	if err := s.Configure(cfg, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(cfg, next); err != nil {
		t.Fatal(err)
	}
	s.WithdrawSSHUpgradeEndpoints(old)
	b, err := s.BootstrapSSH(t.Context(), "replacement")
	if err != nil || b.QUICAddress != next.QUICAddress || b.TLSAddress != next.TLSAddress {
		t.Fatalf("stale withdrawal affected replacement: %+v %v", b, err)
	}
	s.WithdrawSSHUpgradeEndpoints(SSHUpgradeEndpoints{QUICAddress: next.QUICAddress})
	b, err = s.BootstrapSSH(t.Context(), "TLS only")
	if err != nil || b.QUICAddress != "" || b.TLSAddress != next.TLSAddress {
		t.Fatalf("TLS-only bootstrap unavailable: %+v %v", b, err)
	}
	if _, err := s.AuthenticateCredential(t.Context(), b.Credential.Bearer()); err != nil {
		t.Fatal("TLS-only bootstrap credential invalid", err)
	}
	s.WithdrawSSHUpgradeEndpoints(next)
	if _, err := s.BootstrapSSH(t.Context(), "closed"); err == nil {
		t.Fatal("withdrawn endpoint advertised")
	}
	v, err := s.View(t.Context())
	if err != nil || v.Config != cfg {
		t.Fatalf("live publication/withdrawal rewrote requested config: %+v %v", v, err)
	}
}

func TestRemoteSSHConfigureBootstrapAtomicPair(t *testing.T) {
	s, err := OpenRemoteAccess(remotePrivateTestDir(t), RemoteAccessOptions{})
	if err != nil {
		t.Fatal(err)
	}
	configs := []coreprotocol.RemoteAccessConfig{remoteTestConfig(), remoteTestConfig()}
	configs[1].Enabled = false
	configs[1].GracePeriod = time.Hour
	pairs := []SSHUpgradeEndpoints{
		{QUICAddress: "127.0.0.1:17340", TLSAddress: "127.0.0.1:17341"},
		{QUICAddress: "127.0.0.1:17342", TLSAddress: "127.0.0.1:17343"},
	}
	if err := s.Configure(configs[0], pairs[0]); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 200 {
			if err := s.Configure(configs[i%2], pairs[i%2]); err != nil {
				t.Error(err)
				return
			}
		}
	})
	wg.Go(func() {
		for range 200 {
			b, err := s.BootstrapSSH(t.Context(), "concurrent SSH")
			if err != nil {
				t.Error(err)
				return
			}
			if (b.QUICAddress != pairs[0].QUICAddress || b.TLSAddress != pairs[0].TLSAddress) && (b.QUICAddress != pairs[1].QUICAddress || b.TLSAddress != pairs[1].TLSAddress) {
				t.Error("bootstrap mixed endpoint generations", b.QUICAddress, b.TLSAddress)
			}
			s.mu.Lock()
			coherent := (s.config == configs[0] && s.endpoints == pairs[0]) || (s.config == configs[1] && s.endpoints == pairs[1])
			s.mu.Unlock()
			if !coherent {
				t.Error("config and live endpoint generations diverged")
			}
		}
	})
	wg.Wait()
}

func TestRemotePairingConcurrentOneUseAndWriteFailure(t *testing.T) {
	dir := remotePrivateTestDir(t)
	s, err := OpenRemoteAccess(dir, RemoteAccessOptions{Config: remoteTestConfig()})
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.CreatePairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.Pair(t.Context(), coreprotocol.RemotePairRequest{Code: code.Code, DeviceName: "concurrent"})
			if e == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("pair code enrolled %d devices", success)
	}
	if runtime.GOOS == "windows" {
		return
	}
	code, err = s.CreatePairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "devices.json")
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	credential, err := s.Pair(t.Context(), coreprotocol.RemotePairRequest{Code: code.Code, DeviceName: "must not commit"})
	if err == nil || credential.Token != "" {
		t.Fatal("unsafe device registry issued credential")
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	v, err := s.View(t.Context())
	if err != nil || len(v.Devices) != 1 {
		t.Fatalf("failed write changed registry %+v %v", v, err)
	}
}
