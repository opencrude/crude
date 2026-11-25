package core

import (
	"errors"
	"os"
	"path/filepath"

	"crude/workspace/fileio"
)

// The open handle prevents file ID reuse. Stat on that handle reads the ID
// now: Windows path-based Stat may defer the ID lookup until SameFile.
// Share deletion so the observer never prevents a move of the workspace.
type watchRoot struct {
	path   string
	handle *os.File
	info   os.FileInfo
}

var errWatchRootChanged = errors.New("workspace folder changed; reopen it to watch its files")

func (r watchRoot) check() error {
	current, err := os.Lstat(r.path)
	if err != nil {
		return err
	}
	if !current.IsDir() || !os.SameFile(r.info, current) {
		return errWatchRootChanged
	}
	return nil
}

// Usable means the recursive observer covers readable directories. Only an
// observation with no excluded directory can release project diagnostics.
type watchCoverage struct {
	usable, rootLost bool
	excluded         string
}

func (c watchCoverage) complete() bool { return c.usable && c.excluded == "" }

func pinWatchRoot(path string) (watchRoot, error) {
	root := watchRoot{path: filepath.Clean(path)}
	handle, err := fileio.OpenDirectorySharedDelete(root.path)
	if err != nil {
		return watchRoot{}, err
	}
	root.handle = handle
	root.info, err = handle.Stat()
	if err == nil {
		err = root.check()
	}
	if err != nil {
		_ = handle.Close()
		return watchRoot{}, err
	}
	return root, nil
}
