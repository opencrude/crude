//go:build !windows

package fileio

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

func platformOpenBrowser(ctx context.Context, rawURL string) error {
	name, args := desktopCommand(runtime.GOOS, rawURL, false)
	if err := exec.CommandContext(ctx, name, args...).Run(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

func platformOpenPath(path string, reveal bool) error {
	name, args := desktopCommand(runtime.GOOS, path, reveal)
	if output, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("open %s: %w (%s)", filepath.Base(path), err, output)
	}
	return nil
}
