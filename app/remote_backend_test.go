package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/remoteconn"
	"crude/shellport"
	"crude/shellport/remote"
)

func TestRemoteBackendFailedTestNeverSaves(t *testing.T) {
	m, err := newRemoteBackendManager(t.TempDir(), "test-device", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := remoteconn.Connection{ID: "one", Name: "SSH host", Kind: "ssh", Host: "my-alias"}
	calls := 0
	m.ssh = func(got remoteconn.Connection) remote.Dialer {
		if got != c {
			t.Fatal("changed connection")
		}
		return func(context.Context, string) (*coreclient.Client, error) {
			calls++
			return nil, errors.New("host key rejected")
		}
	}
	if _, err = m.Save(t.Context(), shellport.RemoteConnectionDraft{Connection: c}); err == nil {
		t.Fatal("failed connection saved")
	}
	s, err := m.Snapshot(t.Context())
	if err != nil || len(s.Connections) != 0 || calls != 1 {
		t.Fatalf("snapshot=%+v calls=%d err=%v", s, calls, err)
	}
}
func TestRemoteBackendPairRequiresTrustedPinAndNeverRetries(t *testing.T) {
	m, err := newRemoteBackendManager(t.TempDir(), "test-device", nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	m.pair = func(context.Context, string, string, string, string, coreprotocol.RemotePairRequest) (coreprotocol.RemoteCredential, error) {
		calls++
		return coreprotocol.RemoteCredential{}, errors.New("secret-code-must-not-leak")
	}
	c := remoteconn.Connection{ID: "direct", Name: "Direct", Kind: "direct", Host: "127.0.0.1", Port: 4444}
	d := shellport.RemoteConnectionDraft{Connection: c, PairCode: "secret-code-must-not-leak"}
	if _, err = m.Save(t.Context(), d); err == nil || calls != 0 {
		t.Fatal("unpinned pairing attempted")
	}
	d.Connection.Fingerprint = strings.Repeat("a", 64)
	if _, err = m.Save(t.Context(), d); err == nil || calls != 1 || strings.Contains(err.Error(), d.PairCode) {
		t.Fatalf("pair calls=%d err=%v", calls, err)
	}
	s, _ := m.Snapshot(t.Context())
	if len(s.Connections) != 0 {
		t.Fatal("failed pair saved")
	}
}
func TestRemoteBackendChangedAuthorityCannotUseSavedCredential(t *testing.T) {
	m, err := newRemoteBackendManager(t.TempDir(), "device", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := remoteconn.Connection{ID: "direct", Name: "Direct", Kind: "direct", Host: "trusted", Port: 4444, Fingerprint: strings.Repeat("b", 64)}
	if err = m.store.Save(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	c.Host = "attacker"
	if _, err = m.Test(t.Context(), c); err == nil || !strings.Contains(err.Error(), "changed host") {
		t.Fatalf("changed host accepted: %v", err)
	}
	calls := 0
	m.pair = func(context.Context, string, string, string, string, coreprotocol.RemotePairRequest) (coreprotocol.RemoteCredential, error) {
		calls++
		return coreprotocol.RemoteCredential{}, nil
	}
	if _, err = m.Save(t.Context(), shellport.RemoteConnectionDraft{Connection: c, PairCode: "code"}); err == nil || calls != 0 {
		t.Fatal("changed authority overwrote connection")
	}
}
func TestRemoteBackendSSHConfigAliasesAreLiteral(t *testing.T) {
	hosts, err := readSSHConfigHosts(t.Context(), strings.NewReader("Host prod work *.internal !excluded # comment\nHost=prod more\nHost -oProxyCommand=x\n Match host ignored\n"))
	if err != nil || strings.Join(hosts, ",") != "prod,work,more" {
		t.Fatalf("hosts=%v err=%v", hosts, err)
	}
}
