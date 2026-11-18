//go:build !windows

package fileio

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestKillRefStopsOwnedDescendantsButNotOtherGroups(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	directory := t.TempDir()
	selectedFile, neighborFile := filepath.Join(directory, "selected"), filepath.Join(directory, "neighbor")
	selected, err := manager.Start("selected-tree", fmt.Sprintf(`(sleep 1; printf selected > %q) & wait`, selectedFile), directory, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start("other-tree", fmt.Sprintf(`sleep 1; printf neighbor > %q`, neighborFile), directory, 0); err != nil {
		t.Fatal(err)
	}
	if err := manager.KillRef(selected.Ref()); err != nil {
		t.Fatal(err)
	}
	if info := waitAsyncCommand(t, manager, selected.Handle); info.Status != AsyncKilled {
		t.Fatalf("selected tree status=%s", info.Status)
	}
	if info := waitAsyncCommand(t, manager, "other-tree"); info.Status != AsyncCompleted {
		t.Fatalf("independent group was affected: %+v", info)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(selectedFile); !os.IsNotExist(err) {
		t.Fatalf("terminated descendant remained alive: %v", err)
	}
	if data, err := os.ReadFile(neighborFile); err != nil || string(data) != "neighbor" {
		t.Fatalf("unrelated command did not finish: %q, %v", data, err)
	}
}

// A descendant that leaves the command's session (an AppImage's FUSE daemon, a
// daemonizing server) is outside the owned tree but may inherit the output
// pipe. The command must still settle, and the escapee is not signalled.
func TestCompletedCommandSettlesDespiteEscapedPipeHolder(t *testing.T) {
	setsid, err := exec.LookPath("setsid")
	if err != nil {
		t.Skip("setsid unavailable")
	}
	pidFile := filepath.Join(t.TempDir(), "escaped.pid")
	var escaped int
	t.Cleanup(func() {
		if escaped > 0 {
			_ = syscall.Kill(escaped, syscall.SIGKILL)
		}
	})
	manager := NewAsyncCommandManager()
	// The leader returns only once the escapee has left its session.
	command := fmt.Sprintf(`%s sh -c 'echo $$ > %q.tmp; mv %q.tmp %q; exec sleep 30' & while [ ! -s %q ]; do sleep 0.01; done; echo leader-done`, setsid, pidFile, pidFile, pidFile, pidFile)
	if _, err := manager.Start("escaped", command, t.TempDir(), 0); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); escaped == 0 && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if data, err := os.ReadFile(pidFile); err == nil {
			_, _ = fmt.Sscan(string(data), &escaped)
		}
	}
	if escaped == 0 {
		t.Fatal("escaped descendant never started")
	}
	info := waitAsyncCommand(t, manager, "escaped")
	if info.Status != AsyncCompleted || info.ExitCode != 0 {
		t.Fatalf("command whose leader succeeded did not complete: %+v", info)
	}
	output, _, err := manager.ReadOutput("escaped", 0)
	if err != nil || !strings.Contains(output, "leader-done") || !strings.Contains(output, detachedOutputNote) {
		t.Fatalf("output %q (%v) lacks the leader output or the detached-holder note", output, err)
	}
	started := time.Now()
	manager.CloseAndWait()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("CloseAndWait waited %v on a pipe held outside the owned tree", elapsed)
	}
	if err := syscall.Kill(escaped, 0); err != nil {
		t.Fatalf("a process outside the owned tree was signalled: %v", err)
	}
}
