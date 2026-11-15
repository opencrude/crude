package core

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"crude/fileio"
)

// coreWatchWalkFS lets the standard fs.WalkDir keep its ordering, error and
// skip semantics while opening directories with delete sharing. Go's ordinary
// Windows os.ReadDir does not share deletion and can block a build removing a
// directory while the watcher scans it.
type coreWatchWalkFS struct {
	root string
}

func (w coreWatchWalkFS) path(name string) (string, error) {
	local, err := filepath.Localize(name)
	if err != nil {
		return "", &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	return filepath.Join(w.root, local), nil
}

func (w coreWatchWalkFS) open(name string) (*os.File, error) {
	path, err := w.path(name)
	if err != nil {
		return nil, err
	}
	return fileio.OpenDirectorySharedDelete(path)
}

func (w coreWatchWalkFS) Open(name string) (fs.File, error) {
	return w.open(name)
}

func (w coreWatchWalkFS) Stat(name string) (fs.FileInfo, error) {
	path, err := w.path(name)
	if err != nil {
		return nil, err
	}
	// Match filepath.WalkDir: do not follow a root symlink.
	return os.Lstat(path)
}

func (w coreWatchWalkFS) ReadDir(name string) ([]fs.DirEntry, error) {
	file, err := w.open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	entries, err := file.ReadDir(-1)
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return entries, err
}

func walkCoreWatch(root string, visit fs.WalkDirFunc) error {
	return fs.WalkDir(coreWatchWalkFS{root: root}, ".", func(name string, entry fs.DirEntry, err error) error {
		return visit(filepath.Join(root, filepath.FromSlash(name)), entry, err)
	})
}
