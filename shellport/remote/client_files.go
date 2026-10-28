package remote

import (
	"context"
	"path"
	"strings"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/workspace"
)

var _ shellport.ClientFiles = (*Workspace)(nil)
var _ shellport.WorkspaceFolderCreator = (*Workspace)(nil)

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
	var out coreprotocol.ClientDirectoryEntries
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

func (w *Workspace) CreateWorkspaceFolder(ctx context.Context, parent, name string) (string, error) {
	var out coreprotocol.StartupCreateFolderResult
	err := w.call(ctx, coreprotocol.StartupCreateFolder, coreprotocol.StartupCreateFolderRequest{Parent: parent, Name: name}, &out)
	return out.Path, err
}
func (w *Workspace) readClientFile(ctx context.Context, value string, external bool) ([]byte, error) {
	w.connMu.Lock()
	client, id := w.client, w.id
	w.connMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	return client.ReadClientFile(ctx, id, w.CanonicalClientPath(value), external)
}
func (w *Workspace) ReadClientFile(ctx context.Context, value string) ([]byte, error) {
	return w.readClientFile(ctx, value, false)
}
func (w *Workspace) ReadExternalClientFile(ctx context.Context, value string) ([]byte, error) {
	return w.readClientFile(ctx, value, true)
}
func (w *Workspace) ListSavedWorkspaces(ctx context.Context) ([]workspace.WorkspaceInfo, error) {
	var out coreprotocol.WorkspaceInfos
	err := w.call(ctx, coreprotocol.SavedWorkspaces, &coreprotocol.Empty{}, &out)
	return out, err
}
