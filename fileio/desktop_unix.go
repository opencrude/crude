//go:build !windows

package fileio

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func platformOpenBrowser(ctx context.Context, rawURL string) error {
	name, args := desktopCommand(runtime.GOOS, rawURL, false)
	if err := exec.CommandContext(ctx, name, args...).Run(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

func platformOpenPath(ctx context.Context, path string, reveal bool) error {
	name, args := desktopCommand(runtime.GOOS, path, reveal)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	output := &desktopOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("open %s: %w (%s)", filepath.Base(path), errors.Join(err, ctx.Err()), output.data)
	}
	return nil
}

type desktopOutput struct{ data []byte }

func (o *desktopOutput) Write(p []byte) (int, error) {
	o.data = append(o.data, p[:min(len(p), (8<<10)-len(o.data))]...)
	return len(p), nil
}
