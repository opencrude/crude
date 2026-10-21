package coreprotocol

import "crude/workspace"

// Saved-workspace operations run on the selected core host. They do not attach
// the workspace being removed, and never operate on a shell-local data root.
const (
	WorkspaceDeletePreview = "workspace.delete_preview"
	WorkspaceDelete        = "workspace.delete_saved"
	WorkspaceRestore       = "workspace.restore_deleted"
)

type WorkspaceDeletePreviewRequest struct {
	ID         string `json:"id"`
	ProjectDir string `json:"project_dir"`
}

type WorkspaceDeleteRequest struct {
	Token string `json:"token"`
}

// A committed move may have a durability warning. Keep its recovery identity
// even then, so the shell can offer Undo rather than claim nothing happened.
type WorkspaceDeletionResult struct {
	Deleted workspace.DeletedWorkspace `json:"deleted"`
	Warning string                     `json:"warning,omitempty"`
}
