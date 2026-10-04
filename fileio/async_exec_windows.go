//go:build windows

package fileio

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func setCmdSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED}
	if strings.EqualFold(strings.TrimSuffix(filepath.Base(cmd.Path), ".exe"), "cmd") && len(cmd.Args) > 1 {
		// cmd.exe parses its command tail itself, not with CommandLineToArgvW.
		// EscapeArg would turn embedded quotes into literal backslash-quotes.
		args := make([]string, len(cmd.Args)-1)
		for i, arg := range cmd.Args[:len(cmd.Args)-1] {
			args[i] = syscall.EscapeArg(arg)
		}
		cmd.SysProcAttr.CmdLine = strings.Join(args, " ") + ` "` + cmd.Args[len(cmd.Args)-1] + `"`
	}
}

func cancelProcess(c *exec.Cmd) error {
	if c.Process != nil {
		return c.Process.Kill()
	}
	return nil
}
