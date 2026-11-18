//go:build !windows

package fileio

import (
	"io"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

func startInteractiveProcess(cmd *exec.Cmd, tty bool, output io.Writer) (io.WriteCloser, <-chan struct{}, error) {
	if !tty {
		input, err := cmd.StdinPipe()
		if err != nil {
			return nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			_ = input.Close()
			return nil, nil, err
		}
		return input, nil, nil
	}
	// A PTY is a new controlling session, not the pipe runner's Setpgid group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 40})
	if err != nil {
		return nil, nil, err
	}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.Copy(output, terminal) }()
	return terminal, done, nil
}
