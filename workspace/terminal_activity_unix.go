//go:build unix

package workspace

import (
	"errors"

	"golang.org/x/sys/unix"
)

// terminalProgram checks both foreground and background shell work. A shell at
// its prompt can still own a long-running job that must survive a refused stop.
func terminalProgram(t terminalIO) (string, bool, error) {
	p, ok := t.(*unixPTY)
	if !ok || p.file == nil {
		return "", false, errors.New("terminal state is unavailable")
	}
	shell := p.Pid()
	raw, err := p.file.SyscallConn()
	if err != nil {
		return "", false, err
	}
	var group int
	var ioctlErr error
	// Control does not switch the descriptor to blocking mode, unlike Fd.
	if err := raw.Control(func(fd uintptr) { group, ioctlErr = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP) }); err != nil {
		return "", false, err
	}
	if ioctlErr != nil {
		return "", false, ioctlErr
	}
	if group <= 0 {
		return "", false, errors.New("terminal foreground state is unavailable")
	}
	if group == shell {
		return backgroundProgram(shell)
	}
	return processName(group), true, nil
}
