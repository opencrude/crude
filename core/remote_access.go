package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/fileio"
	"crude/remoteconn"
)

var (
	ErrRemoteDisabled   = errors.New("core: direct remote access is disabled")
	ErrRemoteCredential = errors.New("core: invalid or expired remote credential")
	ErrPairingLimited   = errors.New("core: pairing attempts temporarily limited")
)

const DefaultRemoteGrace = 15 * time.Minute
const pairingLifetime = 5 * time.Minute
const bootstrapLifetime = time.Minute

type RemoteAccessOptions struct {
	Config coreprotocol.RemoteAccessConfig
	// Now is for deterministic expiry tests. Production uses time.Now.
	Now func() time.Time
	// Audit receives redacted events after durable commit, outside service locks.
	Audit func(RemoteAuditEvent)
}

// SSHUpgradeEndpoints are live listener facts published by the host, not saved
// user choices. An empty address means that carrier is currently unavailable.
type SSHUpgradeEndpoints struct {
	QUICAddress string
	TLSAddress  string
}
type RemoteAuditEvent struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	DeviceID string    `json:"device_id,omitempty"`
}
type remoteStoredDevice struct {
	View coreprotocol.RemoteDevice `json:"view"`
	Hash string                    `json:"hash"`
}
type remoteRegistry struct {
	Version int                  `json:"version"`
	Devices []remoteStoredDevice `json:"devices"`
	Audit   []RemoteAuditEvent   `json:"audit"`
}
type remoteChallenge struct {
	hash     [32]byte
	expires  time.Time
	failures int
}
type remoteBootstrap struct {
	hash    [32]byte
	expires time.Time
}

// RemoteAccess is host-owned authentication, not a listener or a config owner.
// Call administrative methods only from host-local authenticated commands, and
// BootstrapSSH only over a verified SSH stream. Pair may be exposed before auth
// but its transport must never admit other core methods. Authenticate runs before
// session admission (including resume); CheckDevice fences subsequent commands
// and lets the runtime close already-connected revoked devices immediately.
type RemoteAccess struct {
	mu           sync.Mutex
	file         *remoteconn.PrivateFile
	identity     corewire.Identity
	config       coreprotocol.RemoteAccessConfig
	endpoints    SSHUpgradeEndpoints
	now          func() time.Time
	audit        func(RemoteAuditEvent)
	pairing      remoteChallenge
	attemptStart time.Time
	attempts     int
	bootstraps   map[string]remoteBootstrap
}

// ValidateRemoteAccessConfig is shared with the existing application settings
// owner. Endpoint changes require restarting listeners by that owner.
func ValidateRemoteAccessConfig(c coreprotocol.RemoteAccessConfig) error {
	if c.GracePeriod < 0 || c.GracePeriod > 24*time.Hour {
		return errors.New("core: remote grace must be between zero and 24 hours")
	}
	if c.Enabled && (c.QUICAddress == "" || c.TLSAddress == "") {
		return errors.New("core: direct access requires QUIC and TLS listen addresses")
	}
	for _, address := range []string{c.QUICAddress, c.TLSAddress} {
		if address == "" {
			continue
		}
		_, port, err := net.SplitHostPort(address)
		if err != nil || port == "" {
			return errors.New("core: invalid remote listen address")
		}
		// LookupPort parses numeric/service ports without network activity.
		if _, err = net.LookupPort("tcp", port); err != nil {
			return errors.New("core: invalid remote listen port")
		}
	}
	return nil
}
func OpenRemoteAccess(dir string, opts RemoteAccessOptions) (*RemoteAccess, error) {
	if err := ValidateRemoteAccessConfig(opts.Config); err != nil {
		return nil, err
	}
	f, err := remoteconn.OpenPrivateFile(filepath.Join(dir, "devices.json"))
	if err != nil {
		return nil, err
	}
	id, err := corewire.LoadIdentity(dir)
	if err != nil {
		return nil, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	s := &RemoteAccess{file: f, identity: id, config: opts.Config, now: now, audit: opts.Audit, bootstraps: map[string]remoteBootstrap{}}
	err = s.file.Update(context.Background(), func(raw []byte) ([]byte, error) { _, e := decodeRemoteRegistry(raw); return nil, e })
	if err != nil {
		return nil, err
	}
	return s, nil
}
func (s *RemoteAccess) Identity() corewire.Identity { return s.identity }

// Configure atomically publishes committed policy and its live SSH endpoints.
// Initial options configure policy only; no listener is inferred from settings.
func (s *RemoteAccess) Configure(c coreprotocol.RemoteAccessConfig, endpoints SSHUpgradeEndpoints) error {
	if err := ValidateRemoteAccessConfig(c); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = c
	s.endpoints = endpoints
	if !c.Enabled {
		s.pairing = remoteChallenge{}
	}
	return nil
}

// WithdrawSSHUpgradeEndpoints removes only matching live facts. A delayed failed
// listener cannot withdraw a replacement, and shutdown never rewrites settings.
func (s *RemoteAccess) WithdrawSSHUpgradeEndpoints(expected SSHUpgradeEndpoints) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected.QUICAddress != "" && s.endpoints.QUICAddress == expected.QUICAddress {
		s.endpoints.QUICAddress = ""
	}
	if expected.TLSAddress != "" && s.endpoints.TLSAddress == expected.TLSAddress {
		s.endpoints.TLSAddress = ""
	}
}
func (s *RemoteAccess) GracePeriod() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config.GracePeriod == 0 {
		return DefaultRemoteGrace
	}
	return s.config.GracePeriod
}
func decodeRemoteRegistry(raw []byte) (remoteRegistry, error) {
	if raw == nil {
		return remoteRegistry{Version: 1}, nil
	}
	var r remoteRegistry
	if err := remoteconn.DecodeDocument(raw, &r); err != nil {
		return r, err
	}
	if r.Version != 1 || len(r.Devices) > 1024 || len(r.Audit) > 256 {
		return r, errors.New("core: invalid current-format device registry")
	}
	seen := map[string]bool{}
	for _, d := range r.Devices {
		hash, err := hex.DecodeString(d.Hash)
		if !validRemoteName(d.View.Name) || !validRemoteID(d.View.ID) || seen[d.View.ID] || err != nil || len(hash) != 32 {
			return r, errors.New("core: invalid device registry")
		}
		seen[d.View.ID] = true
	}
	return r, nil
}
func validRemoteName(name string) bool {
	return len(name) > 0 && len(name) <= 256 && utf8.ValidString(name) && strings.TrimSpace(name) != "" && !strings.ContainsFunc(name, unicode.IsControl)
}
func validRemoteID(id string) bool {
	b, e := base64.RawURLEncoding.DecodeString(id)
	return e == nil && len(b) == 18
}
func remoteRandom(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func appendRemoteAudit(r *remoteRegistry, event RemoteAuditEvent) {
	r.Audit = append(r.Audit, event)
	if len(r.Audit) > 256 {
		r.Audit = r.Audit[len(r.Audit)-256:]
	}
}
func (s *RemoteAccess) emit(event RemoteAuditEvent) {
	if s.audit != nil {
		s.audit(event)
	}
}
func (s *RemoteAccess) View(ctx context.Context) (coreprotocol.RemoteAccessView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := coreprotocol.RemoteAccessView{Config: s.config, Fingerprint: s.identity.Fingerprint}
	err := s.file.Update(ctx, func(raw []byte) ([]byte, error) {
		r, e := decodeRemoteRegistry(raw)
		if e != nil {
			return nil, e
		}
		for _, d := range r.Devices {
			v.Devices = append(v.Devices, d.View)
		}
		return nil, nil
	})
	return v, err
}

// CreatePairing replaces the previous code. Its 128 bits of entropy are generated
// by crypto/rand; unlike a six-digit PIN it cannot be brute-forced offline. Only
// its SHA256 is retained, in memory, and it expires after five minutes.
func (s *RemoteAccess) CreatePairing(ctx context.Context) (coreprotocol.RemotePairingCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return coreprotocol.RemotePairingCode{}, err
	}
	if !s.config.Enabled {
		return coreprotocol.RemotePairingCode{}, ErrRemoteDisabled
	}
	code, err := remoteRandom(16)
	if err != nil {
		return coreprotocol.RemotePairingCode{}, err
	}
	expires := s.now().Add(pairingLifetime)
	s.pairing = remoteChallenge{hash: sha256.Sum256([]byte(code)), expires: expires}
	return coreprotocol.RemotePairingCode{Code: code, ExpiresAt: expires, Fingerprint: s.identity.Fingerprint}, nil
}

func (s *RemoteAccess) Pair(ctx context.Context, req coreprotocol.RemotePairRequest) (coreprotocol.RemoteCredential, error) {
	s.mu.Lock()
	credential, event, err := s.pairLocked(ctx, req)
	s.mu.Unlock()
	if event.Kind != "" {
		s.emit(event)
	}
	return credential, err
}
func (s *RemoteAccess) pairLocked(ctx context.Context, req coreprotocol.RemotePairRequest) (coreprotocol.RemoteCredential, RemoteAuditEvent, error) {
	var none coreprotocol.RemoteCredential
	if err := ctx.Err(); err != nil {
		return none, RemoteAuditEvent{}, err
	}
	if !s.config.Enabled {
		return none, RemoteAuditEvent{}, ErrRemoteDisabled
	}
	now := s.now()
	if s.attemptStart.IsZero() || now.Sub(s.attemptStart) >= time.Minute {
		s.attemptStart = now
		s.attempts = 0
	}
	if s.attempts >= 5 {
		return none, RemoteAuditEvent{}, ErrPairingLimited
	}
	s.attempts++
	hash := sha256.Sum256([]byte(req.Code))
	valid := subtle.ConstantTimeCompare(hash[:], s.pairing.hash[:]) == 1 && len(req.Code) <= 128 && now.Before(s.pairing.expires) && s.pairing.failures < 5 && validRemoteName(req.DeviceName)
	event := RemoteAuditEvent{At: now, Kind: "pairing_rejected"}
	var credential coreprotocol.RemoteCredential
	var device remoteStoredDevice
	if valid {
		id, err := remoteRandom(18)
		if err != nil {
			return none, RemoteAuditEvent{}, err
		}
		token, err := remoteRandom(32)
		if err != nil {
			return none, RemoteAuditEvent{}, err
		}
		digest := sha256.Sum256([]byte(token))
		device = remoteStoredDevice{View: coreprotocol.RemoteDevice{ID: id, Name: req.DeviceName, CreatedAt: now}, Hash: hex.EncodeToString(digest[:])}
		credential = coreprotocol.RemoteCredential{DeviceID: id, Token: token, Fingerprint: s.identity.Fingerprint}
		event.Kind = "device_paired"
		event.DeviceID = id
	} else {
		s.pairing.failures++
	}
	err := s.file.Update(ctx, func(raw []byte) ([]byte, error) {
		r, err := decodeRemoteRegistry(raw)
		if err != nil {
			return nil, err
		}
		if valid {
			if len(r.Devices) >= 1024 {
				return nil, errors.New("core: device limit reached")
			}
			r.Devices = append(r.Devices, device)
		}
		appendRemoteAudit(&r, event)
		return json.Marshal(r)
	})
	// Replacement can have committed before directory-sync failed. The code
	// must still be spent: never enroll a second device by replaying it.
	if valid && (err == nil || fileio.WriteCommitted(err)) {
		s.pairing = remoteChallenge{}
	}
	if err != nil {
		return none, RemoteAuditEvent{}, err
	}
	if !valid {
		return none, event, ErrRemoteCredential
	}
	return credential, event, nil
}

type RemotePrincipal struct {
	DeviceID string
	// SSHBootstrap identifies a one-time credential delivered via verified SSH.
	// Its resulting session retains SSH authority, not a durable paired device.
	SSHBootstrap bool
}

// AuthenticateCredential adapts the typed credential to Hello.Credential.
func (s *RemoteAccess) AuthenticateCredential(ctx context.Context, bearer string) (RemotePrincipal, error) {
	id, token, ok := coreprotocol.SplitRemoteBearer(bearer)
	if !ok {
		return RemotePrincipal{}, ErrRemoteCredential
	}
	return s.Authenticate(ctx, id, token)
}

// Authenticate always hashes the presented token and compares fixed-size digests
// using crypto/subtle, including for an unknown/revoked device. Random 256-bit
// bearer tokens need no password KDF; only their SHA256 digests reach disk.
func (s *RemoteAccess) Authenticate(ctx context.Context, id, token string) (RemotePrincipal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RemotePrincipal{}, err
	}
	now := s.now()
	presented := sha256.Sum256([]byte(token))
	if b, ok := s.bootstraps[id]; ok {
		valid := subtle.ConstantTimeCompare(presented[:], b.hash[:]) == 1 && now.Before(b.expires)
		if !valid {
			return RemotePrincipal{}, ErrRemoteCredential
		}
		delete(s.bootstraps, id)
		return RemotePrincipal{SSHBootstrap: true}, nil
	}
	var match bool
	err := s.file.Update(ctx, func(raw []byte) ([]byte, error) {
		r, err := decodeRemoteRegistry(raw)
		if err != nil {
			return nil, err
		}
		var expected [32]byte
		index := -1
		for i, d := range r.Devices {
			if d.View.ID == id {
				decoded, _ := hex.DecodeString(d.Hash)
				copy(expected[:], decoded)
				index = i
			}
		}
		valid := subtle.ConstantTimeCompare(presented[:], expected[:]) == 1
		match = valid && len(token) <= 128 && index >= 0 && r.Devices[index].View.RevokedAt.IsZero() && s.config.Enabled
		if !match {
			return nil, ErrRemoteCredential
		}
		r.Devices[index].View.LastSeen = now
		return json.Marshal(r)
	})
	if err != nil {
		return RemotePrincipal{}, err
	}
	return RemotePrincipal{DeviceID: id}, nil
}

func (s *RemoteAccess) CheckDevice(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return ErrRemoteDisabled
	}
	return s.file.Update(ctx, func(raw []byte) ([]byte, error) {
		r, err := decodeRemoteRegistry(raw)
		if err != nil {
			return nil, err
		}
		for _, d := range r.Devices {
			if d.View.ID == id && d.View.RevokedAt.IsZero() {
				return nil, nil
			}
		}
		return nil, ErrRemoteCredential
	})
}
func (s *RemoteAccess) Revoke(ctx context.Context, id string) error {
	s.mu.Lock()
	event := RemoteAuditEvent{At: s.now(), Kind: "device_revoked", DeviceID: id}
	err := s.file.Update(ctx, func(raw []byte) ([]byte, error) {
		r, err := decodeRemoteRegistry(raw)
		if err != nil {
			return nil, err
		}
		i := slices.IndexFunc(r.Devices, func(d remoteStoredDevice) bool { return d.View.ID == id })
		if i < 0 {
			return nil, ErrRemoteCredential
		}
		if !r.Devices[i].View.RevokedAt.IsZero() {
			return nil, nil
		}
		r.Devices[i].View.RevokedAt = event.At
		appendRemoteAudit(&r, event)
		return json.Marshal(r)
	})
	s.mu.Unlock()
	if err == nil {
		s.emit(event)
	}
	return err
}

func (s *RemoteAccess) BootstrapSSH(ctx context.Context, name string) (coreprotocol.RemoteSSHBootstrapResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return coreprotocol.RemoteSSHBootstrapResult{}, err
	}
	if !validRemoteName(name) || (s.endpoints.QUICAddress == "" && s.endpoints.TLSAddress == "") {
		return coreprotocol.RemoteSSHBootstrapResult{}, errors.New("core: SSH upgrade endpoint unavailable")
	}
	now := s.now()
	for id, b := range s.bootstraps {
		if !now.Before(b.expires) {
			delete(s.bootstraps, id)
		}
	}
	if len(s.bootstraps) >= 256 {
		return coreprotocol.RemoteSSHBootstrapResult{}, errors.New("core: too many SSH upgrades")
	}
	id, err := remoteRandom(18)
	if err != nil {
		return coreprotocol.RemoteSSHBootstrapResult{}, err
	}
	token, err := remoteRandom(32)
	if err != nil {
		return coreprotocol.RemoteSSHBootstrapResult{}, err
	}
	expires := now.Add(bootstrapLifetime)
	s.bootstraps[id] = remoteBootstrap{hash: sha256.Sum256([]byte(token)), expires: expires}
	return coreprotocol.RemoteSSHBootstrapResult{Credential: coreprotocol.RemoteCredential{DeviceID: id, Token: token, Fingerprint: s.identity.Fingerprint}, QUICAddress: s.endpoints.QUICAddress, TLSAddress: s.endpoints.TLSAddress, ExpiresAt: expires}, nil
}
