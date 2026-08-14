package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

// DirectoryEntry is presentation-neutral metadata for one browser row.
type DirectoryEntry struct {
	Name  string
	Path  string
	IsDir bool
}

// DirectoryListing is a canonical directory path and its immediate children.
type DirectoryListing struct {
	Path    string
	Entries []DirectoryEntry
}

// BrowseDirectory reads one directory for a TUI or other workspace client.
// Filtering, sorting, selection, and rendering remain presentation concerns.
func (w *Workspace) BrowseDirectory(path string) (DirectoryListing, error) {
	if path == "" {
		path = w.RootPath
	}
	return BrowseDirectoryPath(path)
}

// BrowseDirectoryPath enumerates an explicit path without retaining a
// Workspace. Async UI requests use it so an unresponsive filesystem call
// cannot keep an obsolete workspace graph alive after a project switch.
func BrowseDirectoryPath(path string) (DirectoryListing, error) {
	return browseDirectoryPathWith(path, os.ReadDir)
}

func browseDirectoryPathWith(
	path string,
	readDir func(string) ([]os.DirEntry, error),
) (DirectoryListing, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return DirectoryListing{}, fmt.Errorf("resolve directory %q: %w", path, err)
	}
	abs = filepath.Clean(abs)
	entries, err := readDir(abs)
	if err != nil {
		return DirectoryListing{Path: abs}, fmt.Errorf("read directory %q: %w", abs, err)
	}
	listing := DirectoryListing{Path: abs, Entries: make([]DirectoryEntry, 0, len(entries))}
	for _, entry := range entries {
		listing.Entries = append(listing.Entries, DirectoryEntry{
			Name: entry.Name(), Path: filepath.Join(abs, entry.Name()), IsDir: entry.IsDir(),
		})
	}
	return listing, nil
}
