//go:build !windows

package lsp

import (
	"os/exec"
	"sync"
	"syscall"
)

type processTree struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	settled bool
	done    chan struct{}
}

func prepareProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func ownProcess(cmd *exec.Cmd) (*processTree, error) {
	t := &processTree{cmd: cmd, done: make(chan struct{})}
	go func() {
		// Observe without reaping: the leader pins the group identity until
		// every owned descendant has received its terminal signal.
		_ = waitProcessExit(cmd.Process.Pid)
		t.mu.Lock()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.settled = true
		t.mu.Unlock()
		_ = cmd.Wait()
		close(t.done)
	}()
	return t, nil
}

func (t *processTree) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.settled {
		_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
	}
}
