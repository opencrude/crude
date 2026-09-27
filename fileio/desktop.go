package fileio

import (
	"os"
	"path/filepath"
)

// OpenDesktopPath opens a local file with its registered application, or its
// containing folder in the file manager. It must run outside the UI thread.
func OpenDesktopPath(path string, reveal bool) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		reveal = false
	}
	return platformOpenPath(path, reveal)
}

func desktopCommand(goos, path string, reveal bool) (string, []string) {
	switch goos {
	case "darwin":
		if reveal {
			return "open", []string{"-R", path}
		}
		return "open", []string{path}
	default:
		if reveal {
			path = filepath.Dir(path)
		}
		return "xdg-open", []string{path}
	}
}
