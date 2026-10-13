package workspace

import (
	"context"
	"errors"
)

// InitializeCoreResources runs in the retained workspace factory worker before
// actor publication. A shell must not be responsible for restoring Notices or
// making configured skills discoverable. Configuration inspects local metadata
// but never connects MCP transports or executes/downloads a capability.
func (w *Workspace) InitializeCoreResources(ctx context.Context) error {
	var errs []error
	if w.Status != nil {
		errs = append(errs, w.Status.Hydrate())
	}
	errs = append(errs, w.ensureCapabilities(ctx))
	if ctx.Err() == nil {
		errs = append(errs, w.ensureExecutions())
	}
	return errors.Join(errs...)
}
