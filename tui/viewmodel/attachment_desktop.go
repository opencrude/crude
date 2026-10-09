package viewmodel

import (
	"context"
	"os"
	"time"

	"crude/fileio"
	"crude/shellport"
)

// clientDesktopOpen captures the owning port before the command runs. Connected
// source paths are never handed to local desktop applications. Explicit opens
// materialize private, bounded bytes and retain them until shell shutdown.
func (m *App) clientDesktopOpen() func(string, bool) error {
	files, remote := m.ws.(shellport.ClientFiles)
	generation := m.workspaceSwitchGen.Load()
	return func(path string, reveal bool) error {
		if !remote {
			return fileio.OpenDesktopPath(path, reveal)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cache, err := os.MkdirTemp("", "crude-desktop-")
		if err != nil {
			return err
		}
		state := asyncIOStateFor(m)
		state.mu.Lock()
		if m.asyncIOStopped.Load() {
			state.mu.Unlock()
			_ = os.RemoveAll(cache)
			return context.Canceled
		}
		state.desktopCaches = append(state.desktopCaches, cache)
		state.mu.Unlock()
		local, err := shellport.CacheClientFile(ctx, files, path, cache, true)
		if err != nil {
			return err
		}
		if generation != m.workspaceSwitchGen.Load() || m.asyncIOStopped.Load() {
			return context.Canceled
		}
		return fileio.OpenDesktopPath(local, reveal)
	}
}
