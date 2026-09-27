//go:build !windows

package fileio

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

func platformOpenPath(path string, reveal bool) error {
	name, args := desktopCommand(runtime.GOOS, path, reveal)
	if output, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("open %s: %w (%s)", filepath.Base(path), err, output)
	}
	return nil
}
