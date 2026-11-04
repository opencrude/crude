package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// temporaryToolStore restores permissions after owners have stopped. The real
// store seals published files, so testing.T's ordinary cleanup cannot unlink
// them on Unix when the suite runs as a non-root user.
func temporaryToolStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			switch {
			case entry.IsDir():
				return os.Chmod(path, 0700)
			case entry.Type().IsRegular():
				return os.Chmod(path, 0600)
			}
			return nil
		})
		if err != nil {
			t.Errorf("make temporary tool store removable: %v", err)
		}
	})
	return root
}
