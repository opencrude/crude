//go:build windows || linux

package desktopnotify

import (
	"bytes"
	"os"
	"path/filepath"

	"crude/workspace/fileio"
)

// iconFile writes the application icon under id.Dir and returns its absolute
// path. An unchanged file is not written again. No icon is not an error:
// the desktop then shows its own default picture.
func iconFile(id Identity) (string, error) {
	if len(id.IconPNG) == 0 || id.Dir == "" {
		return "", nil
	}
	dir, err := filepath.Abs(id.Dir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "icon.png")
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, id.IconPNG) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := fileio.WriteAtomicChecked(path, id.IconPNG, 0o644, nil); err != nil {
		return "", err
	}
	return path, nil
}
