//go:build unix

package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type unixPTY struct {
	file *os.File
	cmd  *exec.Cmd
	wait chan waitResult
}

type waitResult struct {
	code int
	err  error
}

func startTerminalIO(command string, args []string, cwd string, cols, rows int) (terminalIO, error) {
	shell := command
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		args = []string{"-l"}
	}
	cmd := exec.Command(shell, args...)
	cmd.Dir = cwd
	cmd.Env = PTYEnviron(nil)
	ws := &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}
	file, err := pty.StartWithSize(cmd, ws)
	if err != nil {
		return nil, fmt.Errorf("open pty: %w", err)
	}
	io := &unixPTY{file: file, cmd: cmd, wait: make(chan waitResult, 1)}
	go func() {
		err := cmd.Wait()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		io.wait <- waitResult{code: code, err: err}
	}()
	return io, nil
}

func (p *unixPTY) Read(b []byte) (int, error)  { return p.file.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.file.Write(b) }

func (p *unixPTY) SetSize(cols, rows int) error {
	return pty.Setsize(p.file, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// Close tears down the PTY child and its process group. pty.Start makes the
// child a session leader (Setsid+Setctty), so its pgid equals its pid and a
// negative-pid kill reaches descendants that stayed in that group (sh -c
// pipelines, non-job-control children). SIGHUP first for normal terminal
// hangup semantics — shells forward it to their jobs — then SIGKILL after a
// short grace for anything that ignored it.
func (p *unixPTY) Close() error {
	pid := 0
	if p.cmd != nil && p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGHUP)
	}
	var err error
	if p.file != nil {
		err = p.file.Close()
	}
	if pid > 0 {
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			// The Wait goroutine reaps the leader; ESRCH means it is gone.
			if syscall.Kill(pid, 0) != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	return err
}

func (p *unixPTY) Wait() (int, error) {
	res := <-p.wait
	return res.code, res.err
}

func (p *unixPTY) Pid() int {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}
