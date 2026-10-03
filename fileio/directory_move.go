package fileio

import (
	"fmt"
	"path/filepath"
)

// MoveDirectoryNoReplace atomically moves a directory without ever replacing
// an existing destination. Callers own identity checks and the mutation lease.
// WriteCommitted distinguishes a completed move followed by a durability error.
func MoveDirectoryNoReplace(from, to string) error {
	if err := moveDirectoryNoReplace(from, to); err != nil {
		return err
	}
	for _, parent := range []string{filepath.Dir(to), filepath.Dir(from)} {
		if err := syncParentDirectory(parent); err != nil {
			return committedWriteError{err: fmt.Errorf("directory moved; sync %s: %w", parent, err)}
		}
	}
	return nil
}
