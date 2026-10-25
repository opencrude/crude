package fileio

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// OpenDesktopPath opens a local file with its registered application, or its
// containing folder in the file manager. It must run outside the UI thread.
func OpenDesktopPath(path string, reveal bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return OpenDesktopPathContext(ctx, path, reveal)
}

// OpenDesktopPathContext is the cancellation-owned desktop route. It never
// interprets paths as commands, and a cancelled request cannot launch an app.
func OpenDesktopPathContext(ctx context.Context, path string, reveal bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	return platformOpenPath(ctx, path, reveal)
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
