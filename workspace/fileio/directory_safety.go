package fileio

import (
	"fmt"
	"os"
	"path/filepath"
)

// CheckDirectoryNoSymlinks requires an existing directory with no symlink in
// any component. Comparing EvalSymlinks' spelling is not a symlink check:
// Windows also expands ordinary 8.3 names and normalizes their case.
func CheckDirectoryNoSymlinks(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("directory component must not be a symlink: %s", path)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}
