package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"crude/retry"
	captypes "crude/types/capability"
)

// ErrNotRecoverable reports a connection that must be connected explicitly
// (never connected in this core, or a local launch that needs authorization).
var ErrNotRecoverable = errors.New("MCP connection needs an explicit connect")

// Recover reconnects a connection that dropped after working, and starts
// built-in servers on first use. It never launches an unapproved local
// process and never starts an interactive sign-in. Failures back off
// exponentially, so callers can ask on every request without hammering a
// struggling server.
func (manager *Manager) Recover(ctx context.Context, id string) error {
	manager.mu.Lock()
	entry := manager.connections[id]
	if manager.closed || entry == nil || entry.config.Disabled || entry.parked {
		manager.mu.Unlock()
		return ErrNotRecoverable
	}
	if entry.session != nil || entry.state.State == "connecting" {
		manager.mu.Unlock()
		return nil
	}
	config := entry.config
	launchable := entry.state.State != "needs sign-in" && config.Host != "shell" &&
		(config.Transport != "stdio" || config.LaunchApproved)
	eligible := config.IsBuiltin() || launchable && (entry.connectedOnce || durablyGranted(config))
	if !eligible {
		manager.mu.Unlock()
		return ErrNotRecoverable
	}
	if wait := time.Until(entry.retryAt); wait > 0 {
		manager.mu.Unlock()
		return fmt.Errorf("reconnecting in %s", wait.Round(time.Second))
	}
	manager.mu.Unlock()
	err := manager.connect(ctx, id, false)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if current := manager.connections[id]; current == entry {
		if err == nil {
			entry.retryAttempt, entry.retryAt = 0, time.Time{}
		} else if ctx.Err() == nil {
			entry.retryAttempt++
			entry.retryAt = time.Now().Add(retry.Exponential(entry.retryAttempt, time.Second, retry.MaxCooldown))
		}
	}
	return err
}

// durablyGranted reports a connection the user already approved tools on, so
// an active agent run may reconnect it after a restart without being asked to
// activate it again.
func durablyGranted(config captypes.ConnectionConfig) bool {
	for _, grant := range config.Grants {
		if !grant.Disabled && grant.Fingerprint != "" {
			return true
		}
	}
	return false
}
