package core

import (
	"context"
	"time"

	"crude/core/protocol"
	"crude/workspace"
)

// savedWorkspaceActivity reads only retained workspace actors. It does not use
// the saved storage catalog or attach anything. A busy actor cannot hold up
// every other row: it reports the last activity it sampled, and an actor that
// was never sampled is not reported as idle.
func (r *Runtime) savedWorkspaceActivity(ctx context.Context) coreprotocol.WorkspaceActivities {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	out := make(coreprotocol.WorkspaceActivities)
	for _, a := range r.actors() {
		if ctx.Err() != nil {
			break
		}
		readCtx, stop := context.WithTimeout(ctx, 20*time.Millisecond)
		// inspect joins a started callback; the worker alone owns out again
		// after it returns. The callback does not wait on resource workers.
		// The actor's attachment ID and the on-disk catalog ID are separate
		// namespaces.
		id := workspace.WorkspaceID(a.ws.RootPath)
		// A successful inspect stored a fresh sample. A busy actor leaves the
		// previous one, which is still the best available observation.
		_ = a.inspect(readCtx, func() {
			sample := a.ws.RecentActivity()
			a.activity.Store(&sample)
		})
		stop()
		if last := a.activity.Load(); last != nil {
			out[id] = *last
		}
	}
	return out
}
