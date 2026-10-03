//go:build windows

package fileio

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func setCmdSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED}
}

func cancelProcess(c *exec.Cmd) error {
	if c.Process != nil {
		return c.Process.Kill()
	}
	return nil
}
