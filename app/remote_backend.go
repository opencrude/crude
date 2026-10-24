package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/remoteconn"
	"crude/shellport"
	"crude/shellport/remote"
	"crude/types"
	"crude/workspace"
)

// The shell host supplies its authenticated SSH bootstrap/matching-install
// dialer. Local and direct admissions remain independent of the selected core.
type remoteBackendManager struct {
	home, profile string
	store         *remoteconn.Store
	logger        workspace.Logger
	ssh           func(remoteconn.Connection) remote.Dialer
	keyring       remoteconn.Keyring
	pair          func(context.Context, string, string, string, string, coreprotocol.RemotePairRequest) (coreprotocol.RemoteCredential, error)
}

func newRemoteBackendManager(home, profile string, logger workspace.Logger) (*remoteBackendManager, error) {
	store, err := remoteconn.Open(filepath.Join(DataDir(home), "remotes", "connections.json"))
	if err != nil {
		return nil, err
	}
	return &remoteBackendManager{home: home, profile: profile, store: store, logger: logger, pair: coreclient.PairRemote}, nil
}

func (m *remoteBackendManager) Snapshot(ctx context.Context) (remoteconn.Snapshot, error) {
	return m.store.Snapshot(ctx)
}

func (m *remoteBackendManager) secrets(allow bool, notice func(string)) (*remoteconn.Secrets, error) {
	return remoteconn.OpenSecrets(filepath.Join(DataDir(m.home), "remotes"), remoteconn.SecretOptions{Keyring: m.keyring, AllowFileFallback: allow, Notice: notice})
}

func (m *remoteBackendManager) connection(ctx context.Context, id string) (remoteconn.Connection, error) {
	s, err := m.Snapshot(ctx)
	if err != nil {
		return remoteconn.Connection{}, err
	}
	for _, c := range s.Connections {
		if c.ID == id {
			return c, nil
		}
	}
	return remoteconn.Connection{}, remoteconn.ErrNotFound
}

func (m *remoteBackendManager) dial(c remoteconn.Connection, credential string) (remote.Dialer, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c.Kind == "ssh" {
		if m.ssh != nil {
			return m.ssh(c), nil
		}
		// The fixed-command SSH transport is safe, but upgrade/install is a
		// host responsibility. No unverified network fallback is introduced.
		return shellDialer(m.home, CLIFlags{SSHHost: c.Host, SSHUser: c.User, SSHPort: c.Port, SSHIdentity: c.IdentityFile}), nil
	}
	address := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	carrier := func(ctx context.Context) (corewire.Conn, error) {
		if c.Carrier == "tls" {
			return corewire.DialTLS(ctx, address, c.Fingerprint)
		}
		quicCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		conn, err := corewire.DialQUIC(quicCtx, address, c.Fingerprint)
		cancel()
		if err == nil || c.Carrier == "quic" {
			return conn, err
		}
		return corewire.DialTLS(ctx, address, c.Fingerprint)
	}
	return func(ctx context.Context, id string) (*coreclient.Client, error) {
		return coreclient.DialWithOptions(ctx, carrier, id, coreclient.DialOptions{Credential: credential})
	}, nil
}

func remoteProbe(ctx context.Context, dial remote.Dialer) (shellport.RemoteStatus, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return shellport.RemoteStatus{}, err
	}
	client, err := dial(ctx, fmt.Sprintf("remote-test-%x", nonce))
	if err != nil {
		return shellport.RemoteStatus{}, err
	}
	carrier, rtt := client.Carrier()
	err = client.Close()
	return shellport.RemoteStatus{State: "connected", Carrier: string(carrier), RTT: rtt}, err
}

func (m *remoteBackendManager) credential(ctx context.Context, c remoteconn.Connection) (string, string, error) {
	if c.Kind != "direct" {
		return "", "", nil
	}
	notice := ""
	s, err := m.secrets(true, func(text string) { notice = text })
	if err != nil {
		return "", "", err
	}
	value, err := s.Get(ctx, c.ID)
	return value, notice, err
}

func (m *remoteBackendManager) Test(ctx context.Context, c remoteconn.Connection) (shellport.RemoteStatus, error) {
	if c.Kind == "direct" {
		old, err := m.connection(ctx, c.ID)
		if err != nil || !sameRemoteAuthority(old, c) {
			return shellport.RemoteStatus{}, errors.New("pair the exact destination before testing; saved credentials cannot be used on a changed host or fingerprint")
		}
	}
	credential, notice, err := m.credential(ctx, c)
	if err != nil {
		return shellport.RemoteStatus{}, err
	}
	dial, err := m.dial(c, credential)
	if err != nil {
		return shellport.RemoteStatus{}, err
	}
	status, err := remoteProbe(ctx, dial)
	status.Notice = notice
	return status, err
}

func sameRemoteAuthority(a, b remoteconn.Connection) bool {
	return a.Kind == b.Kind && a.Host == b.Host && a.Port == b.Port && a.Fingerprint == b.Fingerprint
}

func (m *remoteBackendManager) Save(ctx context.Context, draft shellport.RemoteConnectionDraft) (shellport.RemoteStatus, error) {
	c := draft.Connection
	if err := c.Validate(); err != nil {
		return shellport.RemoteStatus{}, err
	}
	if draft.PairCode == "" {
		// A changed authority must enroll again, never reuse a credential under
		// another certificate or endpoint merely because its display ID matches.
		old, err := m.connection(ctx, c.ID)
		if c.Kind == "direct" && (err != nil || old.Kind != c.Kind || old.Host != c.Host || old.Port != c.Port || old.Fingerprint != c.Fingerprint) {
			return shellport.RemoteStatus{}, errors.New("pair this direct connection with a new host-generated code before saving")
		}
		status, err := m.Test(ctx, c)
		if err == nil {
			err = m.store.Save(ctx, c)
		}
		return status, err
	}
	if c.Kind != "direct" {
		return shellport.RemoteStatus{}, errors.New("pairing is only available for direct connections")
	}
	if old, err := m.connection(ctx, c.ID); err == nil && !sameRemoteAuthority(old, c) {
		return shellport.RemoteStatus{}, errors.New("add a new connection to pair a different authority; the existing connection and credential are retained")
	} else if err != nil && !errors.Is(err, remoteconn.ErrNotFound) {
		return shellport.RemoteStatus{}, err
	}
	carrier := c.Carrier
	if carrier == "auto" || carrier == "" {
		carrier = "quic"
	}
	paired, err := m.pair(ctx, net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), c.Fingerprint, carrier, "remote-pair", coreprotocol.RemotePairRequest{Code: draft.PairCode, DeviceName: m.profile})
	if err != nil {
		return shellport.RemoteStatus{}, errors.New("pairing failed; verify the host fingerprint and request a new code. Pairing is never retried automatically")
	}
	dial, err := m.dial(c, paired.Bearer())
	if err != nil {
		return shellport.RemoteStatus{}, err
	}
	status, err := remoteProbe(ctx, dial)
	if err != nil {
		return status, errors.New("the paired connection test failed; request a new code before trying again")
	}
	secrets, err := m.secrets(draft.AllowFileFallback, func(text string) { status.Notice = text })
	if err == nil {
		err = secrets.Set(ctx, c.ID, paired.Bearer())
	}
	if err != nil {
		return status, errors.New("device credential could not be saved. Enable private-file storage explicitly if the OS keychain is unavailable, then request a new pairing code")
	}
	if err = m.store.Save(ctx, c); err != nil {
		return status, fmt.Errorf("credential saved, but connection metadata was not saved: %w", err)
	}
	return status, nil
}

func (m *remoteBackendManager) Remove(ctx context.Context, id string) error {
	if id == remoteconn.Local {
		return errors.New("local cannot be removed")
	}
	secrets, err := m.secrets(true, func(string) {})
	if err == nil {
		err = secrets.Delete(ctx, id)
	}
	if err != nil {
		return err
	}
	return m.store.Remove(ctx, id)
}

func (m *remoteBackendManager) Attach(ctx context.Context, id, root string) (shellport.Workspace, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("enter a workspace path on the destination backend")
	}
	dial := remote.Dialer(func(ctx context.Context, clientID string) (*coreclient.Client, error) {
		return ConnectCore(ctx, m.home, clientID)
	})
	namespace := ""
	notice := ""
	if id != remoteconn.Local {
		c, err := m.connection(ctx, id)
		if err != nil {
			return nil, err
		}
		credential, warning, err := m.credential(ctx, c)
		notice = warning
		if err != nil {
			return nil, err
		}
		dial, err = m.dial(c, credential)
		if err != nil {
			return nil, err
		}
		namespace = "remote:" + id
	}
	attached, err := remote.Attach(ctx, remote.Options{Root: root, Dial: dial, Namespace: namespace, ClientID: shellClientID(m.profile, namespace), Logger: m.logger})
	if err == nil && notice != "" {
		attached.AddStartupIssues([]types.OperationIssue{{Title: "Remote credential storage", Summary: notice, Guidance: "Re-pair with an OS keychain to move future device credentials out of private-file storage."}})
	}
	return attached, err
}

func (m *remoteBackendManager) RecordUse(ctx context.Context, id, root string) error {
	return m.store.RecordUse(ctx, id, remoteconn.RecentWorkspace{Path: root, Name: filepath.Base(root), OpenedAt: time.Now()})
}

func (m *remoteBackendManager) SSHConfigHosts(ctx context.Context) ([]string, error) {
	// This is the user's SSH configuration, not a Crude data path.
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(home, ".ssh", "config"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return readSSHConfigHosts(ctx, io.LimitReader(f, 1<<20))
}
func readSSHConfigHosts(ctx context.Context, r io.Reader) ([]string, error) {
	scan := bufio.NewScanner(r)
	seen := map[string]bool{}
	var hosts []string
	for scan.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(scan.Text())
		parts := strings.Fields(line)
		if len(parts) > 0 && strings.HasPrefix(strings.ToLower(parts[0]), "host=") {
			parts = append([]string{"Host", parts[0][5:]}, parts[1:]...)
		}
		if len(parts) < 2 || !strings.EqualFold(parts[0], "Host") {
			continue
		}
		for _, host := range parts[1:] {
			if strings.HasPrefix(host, "#") {
				break
			}
			if strings.ContainsAny(host, "*!?[]\"'\\/") || seen[host] {
				continue
			}
			if (remoteconn.Connection{ID: "alias", Name: "alias", Kind: "ssh", Host: host}).Validate() != nil {
				continue
			}
			seen[host] = true
			hosts = append(hosts, host)
			if len(hosts) == 256 {
				return hosts, nil
			}
		}
	}
	return hosts, scan.Err()
}
