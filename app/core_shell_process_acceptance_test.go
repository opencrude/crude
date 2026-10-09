package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// This explicit opt-in runs independent rendered processes, not Apps sharing a
// test goroutine. It consumes a prebuilt executable and never builds one itself.
// CRUDE_PROCESS_ACCEPTANCE_BINARY=/absolute/crude go test ./app -run '^TestCoreShellProcessAcceptance$' -timeout=15m
func TestCoreShellProcessAcceptance(t *testing.T) {
	binary := os.Getenv("CRUDE_PROCESS_ACCEPTANCE_BINARY")
	if binary == "" {
		t.Skip("set CRUDE_PROCESS_ACCEPTANCE_BINARY to opt into ten real tmux shells")
	}
	if runtime.GOOS != "linux" {
		t.Skip("Linux /proc and private tmux acceptance; not native Windows evidence")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("acceptance binary must be an absolute prebuilt path")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "evidence")
	cmd := exec.CommandContext(t.Context(), "python3", "../scripts/core_shell_acceptance.py", "--binary", binary, "--out", out)
	output, err := cmd.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		if evidence, readErr := os.ReadFile(filepath.Join(out, "result.json")); readErr == nil {
			t.Logf("result: %s", evidence)
		}
		t.Fatalf("real-process acceptance: %v (evidence %s)", err, out)
	}
}
