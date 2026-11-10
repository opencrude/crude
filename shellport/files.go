package shellport

import (
	"context"

	"crude/fileio"
	"crude/workspace"
)

// ProjectFormatter formats saved project files through the core-owned catalog
// formatter and one transactional file owner. It is optional for read-only
// shells and test doubles.
type ProjectFormatter interface {
	FormatProject(context.Context) (workspace.FormatProjectResult, error)
}

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
	// CopyPath copies a file or folder to newPath, which must not exist. Both
	// paths must be absolute and under the workspace root. A failed copy
	// leaves nothing behind.
	CopyPath(oldPath, newPath string) workspace.FileOperationResultMsg
	// DeletePath removes a file or directory at the given path. Directories are
	// removed recursively. The path must be absolute and under the workspace root.
	// The result is returned directly to the caller.
	DeletePath(ctx context.Context, path string, progress func(string)) workspace.FileOperationResultMsg
	// ApplyWorkspaceEdit applies a set of text edits to files on disk.
	// Language edits instead use the immutable ReviewedEdits capability.
	// The result is returned directly to the caller.
	ApplyWorkspaceEdit(edits []workspace.FileEdit) workspace.FileOperationResultMsg
	// StartProjectSearch starts a workspace-rooted search. Completion is published
	// through EventBus as ProjectSearchCompletedMsg; no domain result is returned
	// to the TUI method caller.
	StartProjectSearch(query string, opts fileio.ProjectSearchOptions) uint64
	// CancelProjectSearch stops the active workspace project search.
	CancelProjectSearch()
}
