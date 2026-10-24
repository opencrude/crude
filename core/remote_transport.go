package core

import (
	"context"
	"errors"
	"strings"
	"sync"

	"crude/coreprotocol"
	"crude/corewire"
)

func remotePreference(path []string) bool {
	if len(path) == 0 {
		return true
	}
	if !strings.EqualFold(path[0], "network") {
		return false
	}
	if len(path) == 1 {
		return true
	}
	switch strings.ToLower(path[1]) {
	case "remote_access_enabled", "remote_quic_address", "remote_tls_address", "session_grace_seconds":
		return true
	}
	return false
}

// ConfigureRemoteAccess publishes committed application choices. The host owns
// prepared listener resources; this boundary serializes admission/revocation.
func (r *Runtime) ConfigureRemoteAccess(config coreprotocol.RemoteAccessConfig, endpoints SSHUpgradeEndpoints) error {
	r.remoteMu.Lock()
	defer r.remoteMu.Unlock()
	if r.remote == nil {
		return errors.New("remote admission is not installed")
	}
	if err := r.remote.Configure(config, endpoints); err != nil {
		return err
	}
	r.mu.Lock()
	r.grace = r.remote.GracePeriod()
	var closeDevices []*Connection
	if !config.Enabled {
		for _, peer := range r.sessions {
			peer.mu.Lock()
			device := peer.principal.DeviceID != ""
			peer.mu.Unlock()
			if device {
				closeDevices = append(closeDevices, peer)
			}
		}
	}
	r.mu.Unlock()
	for _, peer := range closeDevices {
		peer.Close()
	}
	return nil
}

// SetRemoteAccess installs the host's single admission owner before serving.
// Configuration and device persistence remain with their existing owners.
func (r *Runtime) SetRemoteAccess(access *RemoteAccess) error {
	if access == nil {
		return errors.New("remote admission owner is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || len(r.clients) != 0 {
		return errors.New("install remote admission before serving clients")
	}
	r.remote = access
	r.grace = access.GracePeriod()
	return nil
}

// RemoteAuthenticator admits a new network session, not a carrier resume.
// A resumed session is authenticated by its independent secret and its retained
// device is rechecked under the runtime's revocation/admission boundary.
func (r *Runtime) RemoteAuthenticator() Authenticator {
	return func(ctx context.Context, hello coreprotocol.Hello) (RemotePrincipal, error) {
		if r.remote == nil || hello.SSHRelay {
			return RemotePrincipal{}, ErrRemoteCredential
		}
		return r.remote.AuthenticateCredential(ctx, hello.Credential)
	}
}

func (c *Connection) dispatchRemote(ctx context.Context, request coreprotocol.Request) (coreprotocol.Response, bool) {
	switch request.Method {
	case coreprotocol.RemoteAccessRead, coreprotocol.RemotePairingCreate, coreprotocol.RemoteDeviceRevoke, coreprotocol.RemoteSSHBootstrap:
	default:
		return coreprotocol.Response{}, false
	}
	service := c.runtime.remote
	if service == nil {
		return fault(request, "remote_unavailable", "remote access is not configured on this core"), true
	}
	c.mu.Lock()
	local, ssh := c.localHost, c.principal.SSHBootstrap
	c.mu.Unlock()
	if !local && (request.Method != coreprotocol.RemoteSSHBootstrap || !ssh) {
		return fault(request, "forbidden", "this operation requires an authenticated local host session"), true
	}
	var value coreprotocol.Value
	var err error
	switch request.Method {
	case coreprotocol.RemoteAccessRead:
		var out coreprotocol.RemoteAccessView
		out, err = service.View(ctx)
		value = &out
	case coreprotocol.RemotePairingCreate:
		var in coreprotocol.Empty
		if err = decodeRequest(request, &in); err == nil {
			var out coreprotocol.RemotePairingCode
			out, err = service.CreatePairing(ctx)
			value = &out
		}
	case coreprotocol.RemoteDeviceRevoke:
		var in coreprotocol.RemoteRevokeRequest
		if err = decodeRequest(request, &in); err == nil {
			c.runtime.remoteMu.Lock()
			err = service.Revoke(ctx, in.DeviceID)
			if err == nil {
				c.runtime.mu.Lock()
				var revoked []*Connection
				for _, peer := range c.runtime.sessions {
					peer.mu.Lock()
					matches := peer.principal.DeviceID == in.DeviceID
					peer.mu.Unlock()
					if matches {
						revoked = append(revoked, peer)
					}
				}
				c.runtime.mu.Unlock()
				for _, peer := range revoked {
					peer.Close()
				}
			}
			c.runtime.remoteMu.Unlock()
		}
		value = &coreprotocol.Empty{}
	case coreprotocol.RemoteSSHBootstrap:
		if !ssh {
			return fault(request, "forbidden", "SSH upgrade requires an authenticated SSH session"), true
		}
		var in coreprotocol.RemoteSSHBootstrapRequest
		if err = decodeRequest(request, &in); err == nil {
			var out coreprotocol.RemoteSSHBootstrapResult
			out, err = service.BootstrapSSH(ctx, in.DeviceName)
			value = &out
		}
	}
	if err != nil {
		return fault(request, "remote_failed", err.Error()), true
	}
	return responseValue(request, value), true
}

func ServeTLS(ctx context.Context, runtime *Runtime, listener *corewire.TLSListener, auth Authenticator) error {
	if auth == nil {
		return errors.New("TLS listener requires remote authentication")
	}
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		workers.Add(1)
		go func() { defer workers.Done(); _ = ServeCarrier(ctx, runtime, conn, auth) }()
	}
}
