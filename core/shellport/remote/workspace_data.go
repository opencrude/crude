package remote

import (
	"context"

	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/workspace"
)

var _ shellport.WorkspaceData = (*Workspace)(nil)

// WorkspaceUsage reads the model use of every session in the core.
func (w *Workspace) WorkspaceUsage(ctx context.Context) (workspace.UsageReport, error) {
	var out coreprotocol.WorkspaceUsageResult
	err := w.callCancellable(ctx, coreprotocol.WorkspaceUsage, &coreprotocol.WorkspaceUsageRequest{}, &out)
	return out.Report, err
}

// WorkspaceStorage measures the workspace folder in the core.
func (w *Workspace) WorkspaceStorage(ctx context.Context) (workspace.StorageReport, error) {
	var out coreprotocol.WorkspaceStorageResult
	err := w.callCancellable(ctx, coreprotocol.WorkspaceStorage, &coreprotocol.WorkspaceStorageRequest{}, &out)
	return out.Report, err
}

// CleanWorkspace runs one Storage cleanup in the core.
func (w *Workspace) CleanWorkspace(ctx context.Context, kind string) (workspace.CleanupResult, error) {
	var out coreprotocol.WorkspaceCleanupResult
	err := w.callCancellable(ctx, coreprotocol.WorkspaceCleanup, &coreprotocol.WorkspaceCleanupRequest{Kind: kind}, &out)
	return out.Result, err
}
