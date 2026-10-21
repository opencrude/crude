package shellport

import (
	"context"

	"crude/coreprotocol"
	"crude/workspace"
)

// SavedWorkspaceManagement is host-owned filesystem and lifetime work. Every
// call runs off Update; connected shells always delegate to their selected core.
type SavedWorkspaceManagement interface {
	PreviewWorkspaceDeletion(context.Context, string, string) (workspace.WorkspaceDeletion, error)
	DeleteSavedWorkspace(context.Context, workspace.WorkspaceDeletion) (workspace.DeletedWorkspace, error)
	RestoreDeletedWorkspace(context.Context, workspace.DeletedWorkspace) error
}

// SavedWorkspaceDeletionReceipts observes a lost acknowledgement without
// replaying deletion or Undo, including after the port reconnects.
type SavedWorkspaceDeletionReceipts interface {
	WorkspaceDeletionReceipt(context.Context, string) (coreprotocol.Receipt, error)
}

func (l *local) PreviewWorkspaceDeletion(ctx context.Context, id, projectDir string) (workspace.WorkspaceDeletion, error) {
	return workspace.PreviewWorkspaceDeletion(ctx, id, projectDir, l.ws.RootPath)
}

func (l *local) DeleteSavedWorkspace(ctx context.Context, plan workspace.WorkspaceDeletion) (workspace.DeletedWorkspace, error) {
	return workspace.DeleteSavedWorkspace(ctx, plan)
}

func (l *local) RestoreDeletedWorkspace(ctx context.Context, deleted workspace.DeletedWorkspace) error {
	return workspace.RestoreDeletedWorkspace(ctx, deleted)
}
