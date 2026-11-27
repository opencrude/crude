package shellport

import (
	"context"

	"crude/workspace"
)

// WorkspaceData reads the workspace's model use and disk use and runs the
// Storage cleanups. Only a shell attached to a core offers it.
type WorkspaceData interface {
	WorkspaceUsage(context.Context) (workspace.UsageReport, error)
	WorkspaceStorage(context.Context) (workspace.StorageReport, error)
	CleanWorkspace(ctx context.Context, kind string) (workspace.CleanupResult, error)
}
