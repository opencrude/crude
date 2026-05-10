package fileio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
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
	Duration  time.Duration // computed at copy time by List/Check
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

type AsyncCommandManager struct {
	mu       sync.Mutex
	commands map[string]*asyncCmdState
}

func NewAsyncCommandManager() *AsyncCommandManager {
	return &AsyncCommandManager{
		commands: make(map[string]*asyncCmdState),
	}
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

	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		// Write to a temp batch file to avoid cmd /C quote-stripping issues.
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
		// Store path for cleanup — AsyncCommandManager doesn't have a hook for
		// this, but we rely on the OS temp cleaner or use a deferred cleanup
		// in a wrapper.  For now, the file is small and will be cleaned by OS.
		c = exec.CommandContext(ctx, "cmd", "/C", batPath)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", command)
	}
	if cwd != "" {
		c.Dir = cwd
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
		return nil, fmt.Errorf("failed to start async command %q: %w", handle, err)
	}

	state.info.PID = c.Process.Pid

	m.mu.Lock()
	m.commands[handle] = state
	m.mu.Unlock()

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

	return &state.info, nil
}

func (m *AsyncCommandManager) Check(handle string) (*AsyncCmdInfo, bool) {
	m.mu.Lock()
	state, ok := m.commands[handle]
	m.mu.Unlock()
	if !ok {
		return nil, false
	}
	state.mu.Lock()
	info := state.info
	state.mu.Unlock()
	info.computeDuration()
	return &info, true
}

func (m *AsyncCommandManager) Kill(handle string) error {
	m.mu.Lock()
	state, ok := m.commands[handle]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("async command handle %q not found", handle)
	}
	state.cancel()
	return nil
}

func (m *AsyncCommandManager) ReadOutput(handle string, sinceByte int) (string, int, error) {
	m.mu.Lock()
	state, ok := m.commands[handle]
	m.mu.Unlock()
	if !ok {
		return "", 0, fmt.Errorf("async command handle %q not found", handle)
	}
	state.mu.Lock()
	total := state.output.Len()
	if sinceByte >= total {
		state.mu.Unlock()
		return "", total, nil
	}
	out := string(state.output.Bytes()[sinceByte:total])
	state.mu.Unlock()
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
	return string(state.output.Bytes()[start:n])
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
	delete(m.commands, handle)
	m.mu.Unlock()
}
