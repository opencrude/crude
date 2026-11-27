package coreprotocol

import "crude/workspace"

// Workspace data methods serve the Workspace panel's Overview and Storage
// tabs. Reads walk the workspace folder in the core; cleanup uses the owner
// of each kind of data.
const (
	WorkspaceUsage   = "workspace.usage"
	WorkspaceStorage = "workspace.storage"
	WorkspaceCleanup = "workspace.cleanup"
)

// WorkspaceUsageRequest asks for the model use of every session.
type WorkspaceUsageRequest struct{}

// WorkspaceUsageResult carries the summed report.
type WorkspaceUsageResult struct{ Report workspace.UsageReport }

// WorkspaceStorageRequest asks for the size of the workspace folder.
type WorkspaceStorageRequest struct{}

// WorkspaceStorageResult carries the measured categories.
type WorkspaceStorageResult struct{ Report workspace.StorageReport }

// WorkspaceCleanupRequest removes one kind of data: workspace.StorageTemporary,
// workspace.StorageHistory or workspace.StorageWorktrees.
type WorkspaceCleanupRequest struct{ Kind string }

// WorkspaceCleanupResult reports what the cleanup removed and kept.
type WorkspaceCleanupResult struct{ Result workspace.CleanupResult }
