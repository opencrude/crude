package shellport

import (
	"context"

	"crude/fileio"
	"crude/workspace"
)

// Files is the filesystem mutation and project-search surface.
type Files interface {
	// Documents is the editor save scheduler; nil until EnsureDocuments.
	Documents() DocumentService
	// EnsureDocuments creates the save scheduler on first use and routes its events to bus.
	EnsureDocuments(bus *workspace.EventBus) DocumentService
	// LSPMgr is the language-server manager; nil when unavailable.
	LSPMgr() LSPService
	// CreateFile creates an empty file at the given path. The path must be
	// absolute and under the workspace root. The result is returned directly to
	// the caller.
	CreateFile(path string) workspace.FileOperationResultMsg
	// CreateFolder creates a directory (and parents) at the given path. The path
	// must be absolute and under the workspace root. The result is returned
	// directly to the caller.
	CreateFolder(path string) workspace.FileOperationResultMsg
	// RenamePath renames a file or folder from oldPath to newPath. Both paths must
	// be absolute and under the workspace root. The result is returned directly to
	// the caller.
	RenamePath(oldPath, newPath string) workspace.FileOperationResultMsg
	// DeletePath removes a file or directory at the given path. Directories are
	// removed recursively. The path must be absolute and under the workspace root.
	// The result is returned directly to the caller.
	DeletePath(path string) workspace.FileOperationResultMsg
	// ApplyWorkspaceEdit applies a set of text edits to files on disk.
	// This is used for LSP rename/refactor operations that modify multiple files.
	// The result is returned directly to the caller.
	ApplyWorkspaceEdit(edits []workspace.FileEdit) workspace.FileOperationResultMsg
	// ApplyLSPRename runs off the UI thread. Its immutable reviewed preimages must
	// match a mandatory recoverable safety snapshot before FileOps may commit.
	ApplyLSPRename(ctx context.Context, edits []workspace.FileEdit) (workspace.FileOperationResultMsg, string)
	// StartProjectSearch starts a workspace-rooted search. Completion is published
	// through EventBus as ProjectSearchCompletedMsg; no domain result is returned
	// to the TUI method caller.
	StartProjectSearch(query string, opts fileio.ProjectSearchOptions) uint64
	// CancelProjectSearch stops the active workspace project search.
	CancelProjectSearch()
}
