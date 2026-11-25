package capability

import (
	"context"
	"errors"

	captypes "crude/types/capability"
)

// admitToolCall checks the mutable service-owned part of the tool grant.
// The connection manager owns the tool fingerprint and transport lifetime.
func (s *Service) admitToolCall(ctx context.Context, actor captypes.RuntimeActor, tool captypes.Tool) (context.Context, error) {
	check := func() error {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if s.closed || s.runs[actorKey(actor)] != taskKey(actor) {
			return errors.New("dynamic request run was revoked or replaced")
		}
		config, ok := s.settings.Connections[tool.ConnectionID]
		if !ok || config.Disabled {
			return errors.New("MCP connection is unavailable or disabled")
		}
		if source, ok := s.settings.Sources[config.Source]; config.Source != "" && (!ok || source.Disabled || !source.Trusted) {
			return errors.New("MCP source is unavailable or untrusted")
		}
		return nil
	}
	if err := check(); err != nil {
		return ctx, err
	}
	return captypes.WithFormGuard(ctx, func(ctx context.Context, id string) error {
		if id != tool.ConnectionID {
			return errors.New("MCP form belongs to another connection")
		}
		if err := s.validateRuntime(ctx, actor); err != nil {
			return err
		}
		return check()
	}), nil
}
