package shellport

import (
	"context"
	"os"
	"path/filepath"

	"crude/coreprotocol"
	"crude/workspace"
)

// ClientDirectoryEntry is detached metadata, including link identity and sorting
// data. It never implies permission to follow a link.
type ClientDirectoryEntry = coreprotocol.ClientDirectoryEntry

// ClientFiles is an optional connected read port. Calls perform remote I/O and
// must run in commands, never Update/Paint. Project methods reject root escapes;
// external methods explicitly invoke trusted same-user read authority.
type ClientFiles interface {
	BrowseDirectory(context.Context, string) ([]ClientDirectoryEntry, error)
	BrowseExternalDirectory(context.Context, string) ([]ClientDirectoryEntry, error)
	CatalogFiles(context.Context, bool) (workspace.BrowserCatalog, error)
	ReadClientFile(context.Context, string) ([]byte, error)
	ReadExternalClientFile(context.Context, string) ([]byte, error)
	ListSavedWorkspaces(context.Context) ([]workspace.WorkspaceInfo, error)
	CanonicalClientPath(string) string
}

// CacheClientFile materializes an explicit client-local desktop preview. The
// caller owns the private cache directory and cleanup; remote paths never become
// local destinations. No desktop application is launched without user action.
func CacheClientFile(ctx context.Context, files ClientFiles, remotePath, cacheDir string, external bool) (string, error) {
	var data []byte
	var err error
	if external {
		data, err = files.ReadExternalClientFile(ctx, remotePath)
	} else {
		data, err = files.ReadClientFile(ctx, remotePath)
	}
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(cacheDir, "crude-preview-*"+filepath.Ext(remotePath))
	if err != nil {
		return "", err
	}
	name := f.Name()
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return name, nil
}
