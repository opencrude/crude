package workspace

import (
	"context"
	"path/filepath"
	"strings"

	"crude/third_party/pathwalk"
)

// BrowserCatalog is the neutral path snapshot shared by the file-tree picker
// and the workspace-folder picker. It contains metadata only.
type BrowserCatalog struct {
	Entries   []pathwalk.Entry
	Visited   int
	Skipped   int
	Errors    int
	Truncated bool
}

// BrowserPathRequest controls one bounded recursive name/path discovery.
type BrowserPathRequest struct {
	Root            string
	DirectoriesOnly bool
	IncludeHidden   bool
	MaxDepth        int
	MaxEntries      int
	MaxPathBytes    int64
}

// ScanBrowserPaths discovers paths for picker and file-tree filtering. It is
// intentionally separate from the agent grep/glob tools and never reads file
// contents.
func ScanBrowserPaths(ctx context.Context, req BrowserPathRequest) (BrowserCatalog, error) {
	cat, err := pathwalk.Scan(ctx, pathwalk.Request{
		Root:            req.Root,
		DirectoriesOnly: req.DirectoriesOnly,
		IncludeHidden:   req.IncludeHidden,
		MaxDepth:        req.MaxDepth,
		MaxWorkers:      pathwalk.DefaultWorkers,
		MaxEntries:      req.MaxEntries,
		MaxPathBytes:    req.MaxPathBytes,
		SkipDir:         defaultBrowserSkipDir,
	})
	return BrowserCatalog{
		Entries: cat.Entries, Visited: cat.Visited, Skipped: cat.Skipped,
		Errors: cat.Errors, Truncated: cat.Truncated,
	}, err
}

// ScanBrowserPathsForWorkspace scans the active workspace root. A workspace
// pointer is used only to provide the root; the scan itself is request-owned
// and context-cancelable.
func (w *Workspace) ScanBrowserPathsForWorkspace(ctx context.Context, directoriesOnly, includeHidden bool) (BrowserCatalog, error) {
	if w == nil {
		return BrowserCatalog{}, context.Canceled
	}
	return ScanBrowserPaths(ctx, BrowserPathRequest{
		Root:            w.RootPath,
		DirectoriesOnly: directoriesOnly,
		IncludeHidden:   includeHidden,
	})
}

// defaultBrowserSkipDir prevents an accidental picker search from descending
// into dependency, build, repository-metadata, and Crude state trees. Users
// can still navigate to an explicit path through the breadcrumb/path field.
func defaultBrowserSkipDir(_ string, name string) bool {
	switch strings.ToLower(filepath.Base(name)) {
	case ".git", ".crude", "node_modules", "vendor", "target", "dist", "build", "out", "coverage":
		return true
	default:
		return false
	}
}
