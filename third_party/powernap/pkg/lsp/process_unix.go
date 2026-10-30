//go:build !windows

package lsp

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type processTree struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	settled  bool
	stopping bool
	killed   bool
	err      error
	signal   func(int, syscall.Signal) error
	done     chan struct{}
}

func prepareProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func ownProcess(cmd *exec.Cmd) (*processTree, error) {
	return ownProcessWith(cmd, waitProcessExit, syscall.Kill), nil
}

func ownProcessWith(cmd *exec.Cmd, observe func(int) error, signal func(int, syscall.Signal) error) *processTree {
	t := &processTree{cmd: cmd, done: make(chan struct{}), signal: signal}
	// Reject an already consumed identity before publishing the owner: Close
	// can race the observer's first instruction.
	t.settled = cmd.ProcessState != nil || cmd.Process.Pid <= 0
	go func() {
		// Observe without reaping: the leader pins the group identity until
		// every owned descendant has received its terminal signal.
		err := observe(cmd.Process.Pid)
		t.mu.Lock()
		if err != nil {
			// Observation failed, so the leader may already have been reaped.
			// Never address a possibly recycled process group from this point.
			t.err = errors.Join(t.err, fmt.Errorf("observe server exit; process-group containment unproven: %w", err))
		} else {
			t.stopLocked()
		}
		t.settled = true
		killed := t.killed
		t.mu.Unlock()
		var cleanupErr error
		if err != nil && !errors.Is(err, syscall.ECHILD) && !errors.Is(err, syscall.ESRCH) {
			// Best-effort cleanup of the direct child when observation is
			// unsupported. ECHILD/ESRCH cannot establish even that identity.
			if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				cleanupErr = fmt.Errorf("kill server after failed observation: %w", killErr)
			} else if killErr == nil {
				killed = true
			}
		}
		if waitErr := cmd.Wait(); waitErr != nil {
			var exit *exec.ExitError
			expected := errors.As(waitErr, &exit) && killed && exit.ProcessState.Sys().(syscall.WaitStatus).Signal() == syscall.SIGKILL
			if !expected {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("reap server: %w", waitErr))
			}
		}
		t.mu.Lock()
		t.err = errors.Join(t.err, cleanupErr)
		t.mu.Unlock()
		close(t.done)
	}()
	return t
}

func (t *processTree) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopLocked()
}

func (t *processTree) stopLocked() {
	if t.settled || t.stopping {
		return
	}
	t.stopping = true
	if err := t.signal(-t.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		if !errors.Is(err, syscall.ESRCH) {
			t.err = errors.Join(t.err, fmt.Errorf("kill server process group; containment unproven: %w", err))
		}
	} else {
		t.killed = true
	}
}

func (t *processTree) wait() error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-t.done:
	case <-timer.C:
		t.mu.Lock()
		defer t.mu.Unlock()
		return errors.Join(t.err, errors.New("server shutdown timed out; process group and reap completion unproven"))
	}
	return t.err
}
