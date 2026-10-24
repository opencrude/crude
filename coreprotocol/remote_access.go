package coreprotocol

import (
	"encoding/base64"
	"strings"
	"time"
)

const (
	RemoteAccessRead    = "remote.access.read"
	RemotePairingCreate = "remote.pairing.create"
	RemoteDeviceRevoke  = "remote.device.revoke"
	RemoteSSHBootstrap  = "remote.ssh.bootstrap"
)

// RemoteAccessConfig is an application-only value owned and persisted by the
// existing settings owner. The service does not create a second config file.
type RemoteAccessConfig struct {
	Enabled     bool          `json:"enabled"`
	QUICAddress string        `json:"quic_address"`
	TLSAddress  string        `json:"tls_address"`
	GracePeriod time.Duration `json:"grace_period"` // zero inherits 15 minutes
}
type RemoteDevice struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	RevokedAt time.Time `json:"revoked_at"`
}

// RemoteAccessView never includes credentials, pairing codes, or their hashes.
type RemoteAccessView struct {
	Config      RemoteAccessConfig `json:"config"`
	Fingerprint string             `json:"fingerprint"`
	Devices     []RemoteDevice     `json:"devices"`
}
type RemotePairingCode struct {
	Code        string    `json:"code"`
	ExpiresAt   time.Time `json:"expires_at"`
	Fingerprint string    `json:"fingerprint"`
}
type RemotePairRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"device_name"`
}
type RemoteCredential struct {
	DeviceID    string `json:"device_id"`
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint"`
}
type RemoteRevokeRequest struct {
	DeviceID string `json:"device_id"`
}
type RemoteSSHBootstrapRequest struct {
	DeviceName string `json:"device_name"`
}

// SSH bootstrap is a typed command over the already authenticated SSH stream,
// not an interpolated remote shell command. Credential expires after one use.
type RemoteSSHBootstrapResult struct {
	Credential  RemoteCredential `json:"credential"`
	QUICAddress string           `json:"quic_address"`
	TLSAddress  string           `json:"tls_address"`
	ExpiresAt   time.Time        `json:"expires_at"`
}

// Bearer is the exact Hello.Credential encoding. The token and ID are base64url
// (no dots); this separator is unambiguous and requires no JSON secret document.
func (v RemoteCredential) Bearer() string { return v.DeviceID + "." + v.Token }
func SplitRemoteBearer(bearer string) (id, token string, ok bool) {
	if len(bearer) > 128 {
		return "", "", false
	}
	id, token, ok = strings.Cut(bearer, ".")
	if !ok || len(id) != 24 || len(token) != 43 {
		return "", "", false
	}
	idBytes, idErr := base64.RawURLEncoding.Strict().DecodeString(id)
	tokenBytes, tokenErr := base64.RawURLEncoding.Strict().DecodeString(token)
	return id, token, idErr == nil && tokenErr == nil && len(idBytes) == 18 && len(tokenBytes) == 32
}

func (RemoteCredential) String() string             { return "[remote credential redacted]" }
func (v RemoteCredential) GoString() string         { return v.String() }
func (RemotePairRequest) String() string            { return "[pair request redacted]" }
func (v RemotePairRequest) GoString() string        { return v.String() }
func (RemotePairingCode) String() string            { return "[pairing code redacted]" }
func (v RemotePairingCode) GoString() string        { return v.String() }
func (RemoteSSHBootstrapResult) String() string     { return "[SSH bootstrap redacted]" }
func (v RemoteSSHBootstrapResult) GoString() string { return v.String() }
