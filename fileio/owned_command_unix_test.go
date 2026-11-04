//go:build !windows

package fileio

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnedCommandRefusesDetachedPipeHolder(t *testing.T) {
	setsid, err := exec.LookPath("setsid")
	if err != nil {
		t.Skip("setsid is unavailable")
	}
	for _, result := range []string{"success", "fail"} {
		t.Run(result, func(t *testing.T) {
			dir := t.TempDir()
			ready, stop, stopped := filepath.Join(dir, "ready"), filepath.Join(dir, "stop"), filepath.Join(dir, "stopped")
			t.Cleanup(func() {
				_ = os.WriteFile(stop, []byte("stop"), 0o600)
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if _, err := os.Stat(stopped); err == nil {
						return
					}
					time.Sleep(5 * time.Millisecond)
				}
				t.Error("detached test process did not stop")
			})
			spec := ownedCommandFixture(t, "escape-parent", setsid, ready, stop, stopped, result)
			_, err := RunOwnedCommand(t.Context(), spec)
			var unverified *CommandTreeUnverifiedError
			if !errors.As(err, &unverified) || !errors.Is(err, exec.ErrWaitDelay) {
				t.Fatalf("detached pipe holder was not reported as uncertain: %v", err)
			}
		})
	}
}
