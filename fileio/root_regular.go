package fileio

import (
	"context"
	"errors"
	"os"
)

// OpenRootRegular opens a regular non-link file within root. The returned file
// belongs to the caller. It rejects a final link and verifies the opened file
// against the observed identity. Root confines parent traversal; this is not a
// sandbox against mount points or mutations by another process with filesystem
// access.
func OpenRootRegular(ctx context.Context, root *os.Root, name string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, errors.New("fileio: a root is required")
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	file, err := openRootRegular(root, name)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, ErrNotRegular
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
