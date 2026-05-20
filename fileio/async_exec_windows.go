//go:build windows

package fileio

import (
	"os/exec"
)

func killProcessGroup(_ int) {
}

func setCmdSysProcAttr(_ *exec.Cmd) {
}

func cancelProcess(c *exec.Cmd) error {
	if c.Process != nil {
		return c.Process.Kill()
	}
	return nil
}
