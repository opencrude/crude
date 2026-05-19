package fileio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"crude/logger"
)

type AsyncStatus string

const (
	AsyncRunning   AsyncStatus = "running"
	AsyncCompleted AsyncStatus = "completed"
	AsyncFailed    AsyncStatus = "failed"
	AsyncKilled    AsyncStatus = "killed"
)

type AsyncCmdInfo struct {
	Handle    string
	Command   string
	PID       int
	Status    AsyncStatus
	StartedAt time.Time
	EndedAt   time.Time
	Duration  time.Duration
	ExitCode  int
	Error     string
}

type asyncCmdState struct {
	cmd    *exec.Cmd
	output bytes.Buffer
	cancel context.CancelFunc
	info   AsyncCmdInfo
	mu     sync.Mutex
}

func (st *asyncCmdState) killGroup() {
	st.mu.Lock()
	pid := st.info.PID
	st.mu.Unlock()
	if pid <= 0 {
		return
	}
	if runtime.GOOS != "windows" {
		syscall.Kill(-pid, syscall.SIGKILL)
	}
	st.cancel()
}

func setProcessGroupCmd(ctx context.Context, name string, arg ...string) *exec.Cmd {
	c := exec.Command(name, arg...)
	if runtime.GOOS != "windows" {
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	c.Cancel = func() error {
		if c.Process != nil {
			if runtime.GOOS != "windows" {
				return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
			}
			return c.Process.Kill()
		}
		return nil
	}
	if ctx != nil {
		c.Args = append([]string{name}, arg...)
	}
	return c
}

type AsyncCommandManager struct {
	mu       sync.Mutex
	commands map[string]*asyncCmdState
	logger   *logger.Logger
}

func NewAsyncCommandManager() *AsyncCommandManager {
	return &AsyncCommandManager{
		commands: make(map[string]*asyncCmdState),
	}
}

func NewAsyncCommandManagerWithLogger(lg *logger.Logger) *AsyncCommandManager {
	return &AsyncCommandManager{
		commands: make(map[string]*asyncCmdState),
		logger:   lg,
	}
}

func (m *AsyncCommandManager) SetLogger(lg *logger.Logger) {
	m.logger = lg
}

func buildCommand(ctx context.Context, command string, cwd string) (*exec.Cmd, context.CancelFunc, error) {
	cancelFn := func() {}

	if runtime.GOOS == "windows" {
		tmp, err := os.CreateTemp("", "crude_*.cmd")
		if err != nil {
			return nil, cancelFn, fmt.Errorf("prepare windows command: %w", err)
		}
		batPath := tmp.Name()
		if _, err := tmp.WriteString("@echo off\r\n" + command + "\r\n"); err != nil {
			tmp.Close()
			os.Remove(batPath)
			return nil, cancelFn, fmt.Errorf("prepare windows command: %w", err)
		}
		tmp.Close()
		c := setProcessGroupCmd(ctx, "cmd", "/C", batPath)
		if cwd != "" {
			c.Dir = cwd
		}
		return c, cancelFn, nil
	}

	c := setProcessGroupCmd(nil, "sh", "-c", command)
	if cwd != "" {
		c.Dir = cwd
	}
	return c, cancelFn, nil
}

func (m *AsyncCommandManager) Start(handle, command, cwd string, timeoutSeconds int) (*AsyncCmdInfo, error) {
	m.mu.Lock()
	if _, ok := m.commands[handle]; ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("async command handle %q already in use; kill it first or use a different handle", handle)
	}
	m.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	if timeoutSeconds > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	}

	c, _, err := buildCommand(ctx, command, cwd)
	if err != nil {
		cancel()
		return nil, err
	}

	state := &asyncCmdState{
		cmd:    c,
		cancel: cancel,
		info: AsyncCmdInfo{
			Handle:    handle,
			Command:   command,
			Status:    AsyncRunning,
			StartedAt: time.Now(),
		},
	}
	c.Stdout = &state.output
	c.Stderr = &state.output

	if err := c.Start(); err != nil {
		cancel()
		if m.logger != nil {
			m.logger.Error("ASYNC", "start_failed", "handle", handle, "error", err.Error())
		}
		return nil, fmt.Errorf("failed to start async command %q: %w", handle, err)
	}

	state.info.PID = c.Process.Pid

	m.mu.Lock()
	m.commands[handle] = state
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Info("ASYNC", "start", "handle", handle, "pid", c.Process.Pid, "timeout", timeoutSeconds)
	}

	go func() {
		err := c.Wait()
		state.mu.Lock()
		defer state.mu.Unlock()
		state.info.EndedAt = time.Now()
		if err != nil {
			if ctx.Err() == context.Canceled {
				state.info.Status = AsyncKilled
				state.info.Error = "killed"
				if m.logger != nil {
					m.logger.Warn("ASYNC", "killed", "handle", handle, "pid", state.info.PID, "duration", time.Since(state.info.StartedAt).Round(time.Second).String())
				}
			} else if ctx.Err() == context.DeadlineExceeded {
				state.info.Status = AsyncKilled
				state.info.Error = "timed out"
				if m.logger != nil {
					m.logger.Warn("ASYNC", "timeout", "handle", handle, "pid", state.info.PID, "timeout_seconds", timeoutSeconds)
				}
			} else {
				state.info.Status = AsyncFailed
				state.info.Error = err.Error()
				if exitErr, ok := err.(*exec.ExitError); ok {
					state.info.ExitCode = exitErr.ExitCode()
				}
				if m.logger != nil {
					m.logger.Warn("ASYNC", "failed", "handle", handle, "pid", state.info.PID, "exit_code", state.info.ExitCode, "error", err.Error())
				}
			}
		} else {
			state.info.Status = AsyncCompleted
			state.info.ExitCode = 0
			if m.logger != nil {
				m.logger.Info("ASYNC", "completed", "handle", handle, "pid", state.info.PID, "duration", time.Since(state.info.StartedAt).Round(time.Second).String())
			}
		}
	}()

	return &state.info, nil
}

func (m *AsyncCommandManager) Check(handle string) (*AsyncCmdInfo, bool) {
	m.mu.Lock()
	state, ok := m.commands[handle]
	m.mu.Unlock()
	if !ok {
		if m.logger != nil {
			m.logger.Debug("ASYNC", "check_not_found", "handle", handle)
		}
		return nil, false
	}
	state.mu.Lock()
	info := state.info
	state.mu.Unlock()
	info.computeDuration()
	if m.logger != nil {
		m.logger.Debug("ASYNC", "check", "handle", handle, "status", info.Status, "pid", info.PID)
	}
	return &info, true
}

func (m *AsyncCommandManager) Kill(handle string) error {
	m.mu.Lock()
	state, ok := m.commands[handle]
	m.mu.Unlock()
	if !ok {
		if m.logger != nil {
			m.logger.Warn("ASYNC", "kill_not_found", "handle", handle)
		}
		return fmt.Errorf("async command handle %q not found", handle)
	}
	if m.logger != nil {
		m.logger.Info("ASYNC", "kill", "handle", handle, "pid", state.info.PID)
	}
	state.killGroup()
	return nil
}

func (m *AsyncCommandManager) ReadOutput(handle string, sinceByte int) (string, int, error) {
	m.mu.Lock()
	state, ok := m.commands[handle]
	m.mu.Unlock()
	if !ok {
		if m.logger != nil {
			m.logger.Warn("ASYNC", "read_output_not_found", "handle", handle)
		}
		return "", 0, fmt.Errorf("async command handle %q not found", handle)
	}
	state.mu.Lock()
	total := state.output.Len()
	if sinceByte >= total {
		state.mu.Unlock()
		if m.logger != nil {
			m.logger.Debug("ASYNC", "read_output_no_new", "handle", handle, "since_byte", sinceByte, "total", total)
		}
		return "", total, nil
	}
	out := string(state.output.Bytes()[sinceByte:total])
	state.mu.Unlock()
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.ReplaceAll(out, "\r", "")
	if m.logger != nil {
		m.logger.Debug("ASYNC", "read_output", "handle", handle, "bytes", len(out), "since_byte", sinceByte, "total", total, "next_byte", total)
	}
	return out, total, nil
}

func (m *AsyncCommandManager) OutputTail(handle string, maxBytes int) string {
	m.mu.Lock()
	state, ok := m.commands[handle]
	m.mu.Unlock()
	if !ok {
		return ""
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	n := state.output.Len()
	if n <= 0 {
		return ""
	}
	if maxBytes <= 0 {
		maxBytes = 250
	}
	start := n - min(n, maxBytes)
	out := string(state.output.Bytes()[start:n])
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.ReplaceAll(out, "\r", "")
	return out
}

func (m *AsyncCommandManager) List() []AsyncCmdInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]AsyncCmdInfo, 0, len(m.commands))
	for _, state := range m.commands {
		state.mu.Lock()
		info := state.info
		state.mu.Unlock()
		info.computeDuration()
		list = append(list, info)
	}
	return list
}

func (info *AsyncCmdInfo) computeDuration() {
	if info.Status == AsyncRunning {
		info.Duration = time.Since(info.StartedAt)
	} else {
		info.Duration = info.EndedAt.Sub(info.StartedAt)
	}
}

func (m *AsyncCommandManager) Cleanup(handle string) {
	m.mu.Lock()
	if _, ok := m.commands[handle]; ok {
		if m.logger != nil {
			m.logger.Debug("ASYNC", "cleanup", "handle", handle)
		}
		delete(m.commands, handle)
	}
	m.mu.Unlock()
}

type UntrackedCmd struct {
	state  *asyncCmdState
	cancel context.CancelFunc
	logger *logger.Logger
}

func (m *AsyncCommandManager) StartUntracked(command, cwd string, timeoutSeconds int) (*UntrackedCmd, error) {
	ctx, cancel := context.WithCancel(context.Background())
	if timeoutSeconds > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	}

	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		tmp, err := os.CreateTemp("", "crude_*.cmd")
		if err != nil {
			cancel()
			return nil, fmt.Errorf("prepare windows command: %w", err)
		}
		batPath := tmp.Name()
		if _, err := tmp.WriteString("@echo off\r\n" + command + "\r\n"); err != nil {
			tmp.Close()
			os.Remove(batPath)
			cancel()
			return nil, fmt.Errorf("prepare windows command: %w", err)
		}
		tmp.Close()
		c = exec.CommandContext(ctx, "cmd", "/C", batPath)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", command)
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	c.Cancel = func() error {
		if c.Process != nil {
			if runtime.GOOS != "windows" {
				return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
			}
			return c.Process.Kill()
		}
		return nil
	}
	if cwd != "" {
		c.Dir = cwd
	}

	state := &asyncCmdState{
		cmd:    c,
		cancel: cancel,
		info: AsyncCmdInfo{
			Command:   command,
			Status:    AsyncRunning,
			StartedAt: time.Now(),
		},
	}
	c.Stdout = &state.output
	c.Stderr = &state.output

	if err := c.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	state.info.PID = c.Process.Pid

	lg := m.logger
	go func() {
		err := c.Wait()
		state.mu.Lock()
		defer state.mu.Unlock()
		state.info.EndedAt = time.Now()
		if err != nil {
			if ctx.Err() == context.Canceled {
				state.info.Status = AsyncKilled
				state.info.Error = "killed"
			} else if ctx.Err() == context.DeadlineExceeded {
				state.info.Status = AsyncKilled
				state.info.Error = "timed out"
			} else {
				state.info.Status = AsyncFailed
				state.info.Error = err.Error()
				if exitErr, ok := err.(*exec.ExitError); ok {
					state.info.ExitCode = exitErr.ExitCode()
				}
			}
		} else {
			state.info.Status = AsyncCompleted
			state.info.ExitCode = 0
		}
	}()

	return &UntrackedCmd{state: state, cancel: cancel, logger: lg}, nil
}

func (uc *UntrackedCmd) PID() int {
	uc.state.mu.Lock()
	defer uc.state.mu.Unlock()
	return uc.state.info.PID
}

func (uc *UntrackedCmd) Status() AsyncStatus {
	uc.state.mu.Lock()
	defer uc.state.mu.Unlock()
	return uc.state.info.Status
}

func (uc *UntrackedCmd) Kill() {
	uc.state.killGroup()
}

func (uc *UntrackedCmd) ReadOutput(sinceByte int) (string, int) {
	uc.state.mu.Lock()
	total := uc.state.output.Len()
	if sinceByte >= total {
		uc.state.mu.Unlock()
		return "", total
	}
	out := string(uc.state.output.Bytes()[sinceByte:total])
	uc.state.mu.Unlock()
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.ReplaceAll(out, "\r", "")
	return out, total
}

func (uc *UntrackedCmd) Info() AsyncCmdInfo {
	uc.state.mu.Lock()
	info := uc.state.info
	uc.state.mu.Unlock()
	info.computeDuration()
	return info
}

func (m *AsyncCommandManager) Promote(uc *UntrackedCmd, handle string) (*AsyncCmdInfo, error) {
	m.mu.Lock()
	if _, ok := m.commands[handle]; ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("async command handle %q already in use; kill it first or use a different handle", handle)
	}
	uc.state.mu.Lock()
	uc.state.info.Handle = handle
	uc.state.mu.Unlock()
	m.commands[handle] = uc.state
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Info("ASYNC", "promote", "handle", handle, "pid", uc.state.info.PID)
	}

	uc.state.mu.Lock()
	info := uc.state.info
	uc.state.mu.Unlock()
	info.computeDuration()
	return &info, nil
}
