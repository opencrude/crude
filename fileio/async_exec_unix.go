//go:build !windows

package fileio

import (
	"os/exec"
	"syscall"
)

func setCmdSysProcAttr(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func setHiddenCmdSysProcAttr(_ *exec.Cmd) {}

func cancelProcess(c *exec.Cmd) error {
	if c.Process != nil {
		if err := c.Process.Signal(syscall.Signal(0)); err != nil {
			return err
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	return nil
}
