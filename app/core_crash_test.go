//go:build !windows

package app

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/retry"
)

// TestMain's production CLI route runs Main, not a simulated recorder. Only the
// exact disposable subprocess receives SIGQUIT; the user's core is never used.
func TestCoreFatalCrashReportAtCLIEntry(t *testing.T) {
	home := coreTestHome(t)
	cmd := exec.Command(os.Args[0], "--core", "--server")
	cmd.Env = append(os.Environ(), buildinfo.HomeEnv+"="+home)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	for {
		if _, err := CoreStatus(ctx, home); err == nil {
			break
		}
		if err := retry.Wait(ctx, 20*time.Millisecond); err != nil {
			t.Fatal("fixture core did not start", err)
		}
	}
	if err := cmd.Process.Signal(syscall.SIGQUIT); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("fatal signal did not terminate the core")
	}
	files, err := filepath.Glob(filepath.Join(DataDir(home), "crashes", "core-*.log"))
	if err != nil || len(files) != 1 {
		t.Fatal("core CLI did not retain its crash report", files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"Crude core crash report", "SIGQUIT", "goroutine", "crude/app.RunCore"} {
		if !strings.Contains(text, want) {
			t.Fatalf("actual core report missing %q:\n%s", want, text)
		}
	}
}
