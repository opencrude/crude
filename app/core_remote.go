package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"crude/core"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/network"
	"crude/workspace"
)

// coreRemote owns listening endpoints and carrier workers, never application
// settings persistence. Its admission owner is installed before local serving.
// Disabling direct access keeps the endpoints available for one-use SSH upgrades.
type coreRemote struct {
	mu      sync.Mutex
	runtime *core.Runtime
	access  *core.RemoteAccess
	logger  workspace.Logger
	ctx     context.Context
	cancel  context.CancelFunc
	stop    func() bool
	workers sync.WaitGroup
	closed  bool
	done    chan struct{}
	err     error
	quic    *remoteEndpoint
	tls     *remoteEndpoint
	pending *remoteListenerChange
	retired []*remoteEndpoint
}

type remoteAcceptor interface {
	Accept(context.Context) (corewire.Conn, error)
	Close() error
}

type remoteEndpoint struct {
	listener    remoteAcceptor
	bindAddress string // requested bind/reuse key; address is the actual socket
	address     string
	kind        string
	retired     bool // mu protects retirement and failed acceptance
	failed      bool
}

// remoteListenerChange reserves endpoints without publishing admission or
// settings. The application's config/accounts lock must cover Prepare, durable
// persistence and Commit (or Abort). Only one preparation can be outstanding.
type remoteListenerChange struct {
	host      *coreRemote
	config    coreprotocol.RemoteAccessConfig
	endpoints core.SSHUpgradeEndpoints
	quic      *remoteEndpoint
	tls       *remoteEndpoint
	newQUIC   bool
	newTLS    bool
	// A reactivated QUIC generation borrows a retained transport. Abort stops
	// just that generation; Commit transfers final-close ownership from this
	// retired endpoint to the new immutable endpoint.
	reusedQUIC *remoteEndpoint
	finished   bool
	result     error
}

func remoteListenerConfig(settings network.Settings) (coreprotocol.RemoteAccessConfig, error) {
	settings = network.Normalize(settings)
	if err := settings.Validate(); err != nil {
		return coreprotocol.RemoteAccessConfig{}, err
	}
	config := coreprotocol.RemoteAccessConfig{
		Enabled: settings.RemoteAccessEnabled, QUICAddress: settings.RemoteQUICAddress,
		TLSAddress: settings.RemoteTLSAddress, GracePeriod: time.Duration(settings.SessionGraceSeconds) * time.Second,
	}
	return config, core.ValidateRemoteAccessConfig(config)
}

// A nil host with an error is fatal: private identity/admission could not be
// installed safely. A non-nil host with an error retains a usable local core and
// can be repaired by a later Prepare/Commit; the caller must surface that error.
func newCoreRemote(ctx context.Context, runtime *core.Runtime, dir string, settings network.Settings, logger workspace.Logger) (*coreRemote, error) {
	access, err := core.OpenRemoteAccess(dir, core.RemoteAccessOptions{})
	if err != nil {
		return nil, fmt.Errorf("initialize private remote identity: %w", err)
	}
	if err := runtime.SetRemoteAccess(access); err != nil {
		return nil, err
	}
	owned, cancel := context.WithCancel(ctx)
	h := &coreRemote{runtime: runtime, access: access, logger: logger, ctx: owned, cancel: cancel, done: make(chan struct{})}
	// Parent cancellation closes prepared sockets as well as committed listeners.
	h.mu.Lock()
	h.stop = context.AfterFunc(ctx, func() { _ = h.Close() })
	h.mu.Unlock()
	change, err := h.Prepare(settings)
	if err == nil {
		err = change.Commit()
	}
	return h, err
}

func (h *coreRemote) recordErrorLocked(err error) {
	h.err = err
	if h.logger != nil {
		h.logger.Warn("REMOTE", "listener_failed", "error", err.Error())
	}
}

// Err reports the most recent rejected preparation or unexpected listener exit.
// Successful publication clears it. The application surfaces errors to shells.
func (h *coreRemote) Err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

func (h *coreRemote) Prepare(settings network.Settings) (*remoteListenerChange, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.ctx.Err() != nil {
		return nil, net.ErrClosed
	}
	if h.pending != nil {
		return nil, errors.New("remote listener change is already prepared")
	}
	config, err := remoteListenerConfig(settings)
	if err != nil {
		h.recordErrorLocked(err)
		return nil, err
	}
	c := &remoteListenerChange{host: h, config: config, quic: h.quic, tls: h.tls}
	quicBind, tlsBind := config.QUICAddress, config.TLSAddress
	if !config.Enabled {
		// Disabled cold profiles never reserve user-configured ports or expose
		// nonloopback listeners. Later disables retain healthy sockets so SSH
		// carriers survive and re-enable cannot collide with our own QUIC port.
		quicBind, tlsBind = "127.0.0.1:0", "127.0.0.1:0"
	}
	needsBind := func(endpoint *remoteEndpoint, desired string) bool {
		return endpoint == nil || endpoint.failed || (config.Enabled && endpoint.bindAddress != desired && endpoint.address != desired)
	}
	if needsBind(c.quic, quicBind) {
		var listener *corewire.QUICListener
		for _, endpoint := range h.retired {
			if endpoint.kind == "QUIC" && !endpoint.failed && (endpoint.bindAddress == quicBind || endpoint.address == quicBind) {
				c.reusedQUIC = endpoint
				listener, err = endpoint.listener.(*corewire.QUICListener).PrepareAccept()
				break
			}
		}
		if c.reusedQUIC == nil {
			listener, err = corewire.ListenQUIC(quicBind, h.access.Identity())
		}
		if err == nil {
			c.quic = &remoteEndpoint{listener: listener, bindAddress: quicBind, address: listener.Addr().String(), kind: "QUIC"}
			c.newQUIC = true
		}
	}
	if err == nil && needsBind(c.tls, tlsBind) {
		var listener *corewire.TLSListener
		listener, err = corewire.ListenTLS(tlsBind, h.access.Identity())
		if err == nil {
			c.tls = &remoteEndpoint{listener: listener, bindAddress: tlsBind, address: listener.Addr().String(), kind: "TLS"}
			c.newTLS = true
		}
	}
	if err != nil {
		c.closePreparedLocked()
		err = fmt.Errorf("prepare remote listeners: %w", err)
		h.recordErrorLocked(err)
		return nil, err
	}
	c.endpoints = core.SSHUpgradeEndpoints{QUICAddress: c.quic.address, TLSAddress: c.tls.address}
	h.pending = c
	return c, nil
}

func (c *remoteListenerChange) closePreparedLocked() {
	if c.newQUIC {
		if c.reusedQUIC != nil {
			_ = c.quic.listener.(*corewire.QUICListener).StopAccept()
		} else {
			_ = c.quic.listener.Close()
		}
	}
	if c.newTLS {
		_ = c.tls.listener.Close()
	}
}

// Abort releases only this transaction's reserved sockets. It never closes
// shared existing endpoints and has no effect after a successful Commit.
func (c *remoteListenerChange) Abort() {
	h := c.host
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.finished {
		return
	}
	c.finished, c.result = true, errors.New("remote listener change was aborted")
	c.closePreparedLocked()
	h.pending = nil
}

// Commit must follow durable application persistence. All fallible binding and
// validation happened in Prepare; this publishes the admission policy and swaps
// acceptance ownership without cancelling established carriers on old endpoints.
func (c *remoteListenerChange) Commit() error {
	h := c.host
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.finished {
		return c.result
	}
	c.finished = true
	h.pending = nil
	if h.closed || h.ctx.Err() != nil {
		c.closePreparedLocked()
		c.result = net.ErrClosed
		return c.result
	}
	// A reused listener can fail between Prepare and durable Save. Publish only
	// still-live endpoints; retain its observable failure until a later repair.
	if c.quic.failed {
		c.endpoints.QUICAddress = ""
	}
	if c.tls.failed {
		c.endpoints.TLSAddress = ""
	}
	if err := h.runtime.ConfigureRemoteAccess(c.config, c.endpoints); err != nil {
		c.closePreparedLocked()
		c.result = err
		h.recordErrorLocked(err)
		return err
	}
	oldQUIC, oldTLS := h.quic, h.tls
	h.quic, h.tls = c.quic, c.tls
	if c.reusedQUIC != nil {
		for i, endpoint := range h.retired {
			if endpoint == c.reusedQUIC {
				h.retired = append(h.retired[:i], h.retired[i+1:]...)
				break
			}
		}
	}
	if !c.quic.failed && !c.tls.failed {
		h.err = nil
	}
	// Reusing an ephemeral socket by its actual address must not mutate the
	// source endpoint during Prepare: an aborted save changes no reuse keys.
	if c.config.Enabled {
		if c.quic.address == c.config.QUICAddress {
			c.quic.bindAddress = c.config.QUICAddress
		}
		if c.tls.address == c.config.TLSAddress {
			c.tls.bindAddress = c.config.TLSAddress
		}
	}
	if c.newQUIC {
		h.startLocked(c.quic)
	}
	if c.newTLS {
		h.startLocked(c.tls)
	}
	for _, old := range []*remoteEndpoint{oldQUIC, oldTLS} {
		if old != nil && old != c.quic && old != c.tls {
			old.retired = true
			if listener, ok := old.listener.(interface{ StopAccept() error }); ok {
				_ = listener.StopAccept()
				h.retired = append(h.retired, old)
			} else {
				_ = old.listener.Close()
			}
		}
	}
	return nil
}

func (h *coreRemote) startLocked(endpoint *remoteEndpoint) {
	h.workers.Add(1)
	go func() {
		defer h.workers.Done()
		for {
			conn, err := endpoint.listener.Accept(h.ctx)
			h.mu.Lock()
			if err != nil {
				if !h.closed && !endpoint.retired && h.ctx.Err() == nil {
					endpoint.failed = true
					h.access.WithdrawSSHUpgradeEndpoints(endpoint.upgradeEndpoints())
					h.recordErrorLocked(fmt.Errorf("remote %s listener stopped: %w", endpoint.kind, err))
				}
				h.mu.Unlock()
				return
			}
			if h.closed || endpoint.retired || h.ctx.Err() != nil {
				h.mu.Unlock()
				_ = conn.Close()
				continue
			}
			h.workers.Add(1)
			h.mu.Unlock()
			go func() {
				defer h.workers.Done()
				_ = core.ServeCarrier(h.ctx, h.runtime, conn, h.runtime.RemoteAuthenticator())
			}()
		}
	}()
}

func (e *remoteEndpoint) upgradeEndpoints() core.SSHUpgradeEndpoints {
	if e.kind == "QUIC" {
		return core.SSHUpgradeEndpoints{QUICAddress: e.address}
	}
	return core.SSHUpgradeEndpoints{TLSAddress: e.address}
}

// Close cancels and joins every carrier and accept worker, including carriers on
// retired addresses. Runtime remains owned by RunCore and is closed separately.
func (h *coreRemote) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		<-h.done
		return nil
	}
	h.closed = true
	var endpoints core.SSHUpgradeEndpoints
	for _, endpoint := range []*remoteEndpoint{h.quic, h.tls} {
		if endpoint != nil {
			if endpoint.kind == "QUIC" {
				endpoints.QUICAddress = endpoint.address
			} else {
				endpoints.TLSAddress = endpoint.address
			}
		}
	}
	h.access.WithdrawSSHUpgradeEndpoints(endpoints)
	h.cancel()
	if h.stop != nil {
		h.stop()
	}
	if h.pending != nil {
		c := h.pending
		c.finished, c.result = true, net.ErrClosed
		c.closePreparedLocked()
		h.pending = nil
	}
	for _, endpoint := range []*remoteEndpoint{h.quic, h.tls} {
		if endpoint != nil {
			endpoint.retired = true
			_ = endpoint.listener.Close()
		}
	}
	for _, endpoint := range h.retired {
		_ = endpoint.listener.Close()
	}
	h.retired = nil
	h.mu.Unlock()
	h.workers.Wait()
	close(h.done)
	return nil
}
