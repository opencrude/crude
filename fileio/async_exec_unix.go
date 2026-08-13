//go:build !windows

package fileio

import (
	"os/exec"
	"syscall"
)

func killProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func setCmdSysProcAttr(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func cancelProcess(c *exec.Cmd) error {
	if c.Process != nil {
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	return nil
}
