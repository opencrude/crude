//go:build !windows

package fileio

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

func ownProcessTree(cmd *exec.Cmd) (*ownedCommandTree, error) {
	var mutex sync.Mutex
	stopped, reaped := false, false
	kill := func() error {
		if reaped {
			return os.ErrProcessDone
		}
		if stopped {
			return nil
		}
		if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
			return err
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == nil || errors.Is(err, syscall.ESRCH) {
			stopped = true
			return nil
		}
		return err
	}
	return &ownedCommandTree{
		kill: func() error {
			mutex.Lock()
			defer mutex.Unlock()
			return kill()
		},
		wait: func() error {
			if err := waitCommandExit(cmd.Process.Pid); err != nil {
				mutex.Lock()
				_ = kill()
				reaped = true
				mutex.Unlock()
				_ = cmd.Wait()
				return err
			}
			mutex.Lock()
			_ = kill()
			reaped = true
			mutex.Unlock()
			return cmd.Wait()
		},
		close: func() {},
	}, nil
}

func resumeOwnedProcess(_ *exec.Cmd) error { return nil }
