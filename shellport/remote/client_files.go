package remote

import (
	"context"
	"path"
	"strings"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/workspace"
)

var _ shellport.ClientFiles = (*Workspace)(nil)

// CanonicalClientPath is lexical and uses the core root, never local Getwd/Abs.
// Slash normalization also preserves drive-qualified core paths on Unix clients.
func (w *Workspace) CanonicalClientPath(value string) string {
	root := strings.ReplaceAll(w.root, "\\", "/")
	value = strings.ReplaceAll(value, "\\", "/")
	if value == "" {
		return path.Clean(root)
	}
	if strings.HasPrefix(value, "/") || (len(value) >= 3 && value[1] == ':' && value[2] == '/') {
		return path.Clean(value)
	}
	return path.Join(root, value)
}

func (w *Workspace) browseClientDirectory(ctx context.Context, value string, external bool) ([]shellport.ClientDirectoryEntry, error) {
	method := coreprotocol.ClientFilesBrowse
	if external {
		method = coreprotocol.ClientFilesBrowseExternal
	}
	var out []shellport.ClientDirectoryEntry
	err := w.call(ctx, method, coreprotocol.FilePathRequest{Path: w.CanonicalClientPath(value)}, &out)
	return out, err
}
func (w *Workspace) BrowseDirectory(ctx context.Context, value string) ([]shellport.ClientDirectoryEntry, error) {
	return w.browseClientDirectory(ctx, value, false)
}
func (w *Workspace) BrowseExternalDirectory(ctx context.Context, value string) ([]shellport.ClientDirectoryEntry, error) {
	return w.browseClientDirectory(ctx, value, true)
}
func (w *Workspace) CatalogFiles(ctx context.Context, includeHidden bool) (workspace.BrowserCatalog, error) {
	var out workspace.BrowserCatalog
	err := w.call(ctx, coreprotocol.FilesCatalog, coreprotocol.FileCatalogRequest{IncludeHidden: includeHidden}, &out)
	return out, err
}

// CatalogFolders is request-owned navigation, not a project search catalog.
// The specified root is resolved on the core host, including over SSH.
func (w *Workspace) CatalogFolders(ctx context.Context, root string, includeHidden bool) (workspace.BrowserCatalog, error) {
	var out workspace.BrowserCatalog
	err := w.call(ctx, coreprotocol.StartupCatalog, coreprotocol.StartupCatalogRequest{Root: root, IncludeHidden: includeHidden}, &out)
	return out, err
}
func (w *Workspace) readClientFile(ctx context.Context, value string, external bool) ([]byte, error) {
	method := coreprotocol.FilesRead
	if external {
		method = coreprotocol.ClientFilesReadExternal
	}
	return coreclient.ReadBoundedClientFile(ctx, w.CanonicalClientPath(value), func(ctx context.Context, in coreprotocol.FileReadRequest) (coreprotocol.FileChunk, error) {
		var out coreprotocol.FileChunk
		err := w.call(ctx, method, in, &out)
		return out, err
	})
}
func (w *Workspace) ReadClientFile(ctx context.Context, value string) ([]byte, error) {
	return w.readClientFile(ctx, value, false)
}
func (w *Workspace) ReadExternalClientFile(ctx context.Context, value string) ([]byte, error) {
	return w.readClientFile(ctx, value, true)
}
func (w *Workspace) ListSavedWorkspaces(ctx context.Context) ([]workspace.WorkspaceInfo, error) {
	var out []workspace.WorkspaceInfo
	err := w.call(ctx, coreprotocol.SavedWorkspaces, struct{}{}, &out)
	return out, err
}
