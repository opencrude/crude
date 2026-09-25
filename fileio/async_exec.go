package fileio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
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
	cmd              *exec.Cmd
	output           bytes.Buffer
	cancel           context.CancelFunc
	info             AsyncCmdInfo
	done             chan struct{}
	cleanup          func()
	mu               sync.Mutex
	outputMu         sync.Mutex
	input            io.WriteCloser
	interactionLease chan struct{}
	outputCursor     int
}

func (st *asyncCmdState) killGroup() {
	st.mu.Lock()
	pid := st.info.PID
	st.mu.Unlock()
	if pid <= 0 {
		return
	}
	killProcessGroup(pid)
	st.cancel()
}

func (st *asyncCmdState) Write(data []byte) (int, error) {
	st.outputMu.Lock()
	n, err := st.output.Write(data)
	st.outputMu.Unlock()
	return n, err
}

func setProcessGroupCmd(ctx context.Context, name string, arg ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, arg...)
	setCmdSysProcAttr(c)
	c.Cancel = func() error {
		return cancelProcess(c)
	}
	return c
}

type AsyncCommandManager struct {
	mu          sync.Mutex
	commands    map[string]*asyncCmdState
	reserved    map[string]bool
	autoCounter int
	closing     bool
	startWg     sync.WaitGroup
	logger      *logger.Logger
}

func NewAsyncCommandManager() *AsyncCommandManager {
	return &AsyncCommandManager{
		commands: make(map[string]*asyncCmdState),
		reserved: make(map[string]bool),
	}
}

func NewAsyncCommandManagerWithLogger(lg *logger.Logger) *AsyncCommandManager {
	return &AsyncCommandManager{
		commands: make(map[string]*asyncCmdState),
		reserved: make(map[string]bool),
		logger:   lg,
	}
}

func (m *AsyncCommandManager) SetLogger(lg *logger.Logger) {
	m.logger = lg
}

func buildCommand(ctx context.Context, command string, cwd string) (*exec.Cmd, func(), error) {
	if runtime.GOOS == "windows" {
		tmp, err := os.CreateTemp("", "crude_*.cmd")
		if err != nil {
			return nil, nil, fmt.Errorf("prepare windows command: %w", err)
		}
		batPath := tmp.Name()
		if _, err := tmp.WriteString("@echo off\r\n" + command + "\r\n"); err != nil {
			tmp.Close()
			os.Remove(batPath)
			return nil, nil, fmt.Errorf("prepare windows command: %w", err)
		}
		tmp.Close()
		c := setProcessGroupCmd(ctx, "cmd", "/C", batPath)
		if cwd != "" {
			c.Dir = cwd
		}
		return c, func() { _ = os.Remove(batPath) }, nil
	}

	shell, args := shellForExec()
	c := setProcessGroupCmd(ctx, shell, append(args, command)...)
	if cwd != "" {
		c.Dir = cwd
	}
	return c, func() {}, nil
}

func (m *AsyncCommandManager) Start(handle, command, cwd string, timeoutSeconds int) (*AsyncCmdInfo, error) {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return nil, errors.New("async command manager is closing")
	}
	if _, ok := m.commands[handle]; ok || m.reserved[handle] {
		m.mu.Unlock()
		return nil, fmt.Errorf("async command handle %q already in use; kill it first or use a different handle", handle)
	}
	m.reserved[handle] = true
	m.startWg.Add(1)
	m.mu.Unlock()
	defer m.startWg.Done()
	releaseReservation := func() {
		m.mu.Lock()
		delete(m.reserved, handle)
		m.mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	if timeoutSeconds > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	}

	c, cleanup, err := buildCommand(ctx, command, cwd)
	if err != nil {
		cancel()
		releaseReservation()
		return nil, err
	}

	state := &asyncCmdState{
		cmd:     c,
		cancel:  cancel,
		done:    make(chan struct{}),
		cleanup: cleanup,
		info: AsyncCmdInfo{
			Handle:    handle,
			Command:   command,
			Status:    AsyncRunning,
			StartedAt: time.Now(),
		},
	}
	c.Stdout = state
	c.Stderr = state

	if err := c.Start(); err != nil {
		cancel()
		cleanup()
		releaseReservation()
		if m.logger != nil {
			m.logger.Error("ASYNC", "start_failed", "handle", handle, "error", err.Error())
		}
		return nil, fmt.Errorf("failed to start async command %q: %w", handle, err)
	}
	state.info.PID = c.Process.Pid
	treeCleanup, err := ownProcessTree(c)
	if err != nil {
		state.killGroup()
		_ = c.Wait()
		cleanup()
		releaseReservation()
		return nil, err
	}
	state.cleanup = func() { treeCleanup(); cleanup() }
	if err := resumeOwnedProcess(c); err != nil {
		state.cleanup()
		state.killGroup()
		_ = c.Wait()
		releaseReservation()
		return nil, err
	}

	m.mu.Lock()
	m.commands[handle] = state
	delete(m.reserved, handle)
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Info("ASYNC", "start", "handle", handle, "pid", c.Process.Pid, "timeout", timeoutSeconds)
	}

	go func() {
		defer close(state.done)
		defer state.cleanup()
		err := c.Wait()
		// A shell can exit while descendants continue in its process group.
		// Terminate the group at the ownership boundary before publishing a
		// terminal status so no hidden writer survives the handle.
		killProcessGroup(state.info.PID)
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

	state.mu.Lock()
	info := state.info
	state.mu.Unlock()
	return &info, nil
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
	state.outputMu.Lock()
	total := state.output.Len()
	if sinceByte >= total {
		state.outputMu.Unlock()
		if m.logger != nil {
			m.logger.Debug("ASYNC", "read_output_no_new", "handle", handle, "since_byte", sinceByte, "total", total)
		}
		return "", total, nil
	}
	out := string(state.output.Bytes()[sinceByte:total])
	state.outputMu.Unlock()
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
	state.outputMu.Lock()
	defer state.outputMu.Unlock()
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
	return m.startUntracked(command, cwd, timeoutSeconds, nil)
}

func (m *AsyncCommandManager) startUntracked(command, cwd string, timeoutSeconds int, interactive *InteractiveOptions) (*UntrackedCmd, error) {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return nil, errors.New("async command manager is closing")
	}
	m.startWg.Add(1)
	m.mu.Unlock()
	defer m.startWg.Done()
	ctx, cancel := context.WithCancel(context.Background())
	if timeoutSeconds > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	}

	var c *exec.Cmd
	var cleanup func()
	var err error
	if interactive == nil {
		c, cleanup, err = buildCommand(ctx, command, cwd)
	} else {
		c, cleanup, err = buildInteractiveCommand(ctx, *interactive)
	}
	if err != nil {
		cancel()
		return nil, err
	}

	state := &asyncCmdState{
		cmd:     c,
		cancel:  cancel,
		done:    make(chan struct{}),
		cleanup: cleanup,
		info: AsyncCmdInfo{
			Command:   command,
			Status:    AsyncRunning,
			StartedAt: time.Now(),
		},
	}
	c.Stdout = state
	c.Stderr = state

	var outputDone <-chan struct{}
	start := c.Start
	if interactive != nil {
		state.interactionLease = make(chan struct{}, 1)
		start = func() error {
			var startErr error
			state.input, outputDone, startErr = startInteractiveProcess(c, interactive.TTY, state)
			return startErr
		}
	}
	if err := start(); err != nil {
		cancel()
		cleanup()
		return nil, fmt.Errorf("failed to start command: %w", err)
	}
	state.info.PID = c.Process.Pid
	treeCleanup, err := ownProcessTree(c)
	if err != nil {
		state.killGroup()
		_ = c.Wait()
		if state.input != nil {
			_ = state.input.Close()
		}
		if outputDone != nil {
			<-outputDone
		}
		cleanup()
		return nil, err
	}
	state.cleanup = func() {
		if state.input != nil {
			_ = state.input.Close()
		}
		treeCleanup()
		cleanup()
	}
	if err := resumeOwnedProcess(c); err != nil {
		state.killGroup()
		_ = c.Wait()
		state.cleanup()
		if outputDone != nil {
			<-outputDone
		}
		return nil, err
	}

	lg := m.logger
	go func() {
		defer close(state.done)
		defer state.cleanup()
		err := c.Wait()
		if outputDone != nil {
			// Closing a PTY after process exit is safe after its final output drains.
			select {
			case <-outputDone:
			case <-time.After(time.Second):
				_ = state.input.Close()
				<-outputDone
			}
		}
		killProcessGroup(state.info.PID)
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

func (uc *UntrackedCmd) Done() <-chan struct{} { return uc.state.done }

func (uc *UntrackedCmd) ReadOutput(sinceByte int) (string, int) {
	uc.state.outputMu.Lock()
	total := uc.state.output.Len()
	if sinceByte >= total {
		uc.state.outputMu.Unlock()
		return "", total
	}
	out := string(uc.state.output.Bytes()[sinceByte:total])
	uc.state.outputMu.Unlock()
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

// NextAutoHandle allocates the next manager-unique handle for a command
// promoted out of synchronous execution. Handles are never reused, so a
// promoted command stays addressable for the lifetime of the manager.
func (m *AsyncCommandManager) NextAutoHandle() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		m.autoCounter++
		handle := fmt.Sprintf("auto-%d", m.autoCounter)
		if _, ok := m.commands[handle]; ok {
			continue
		}
		if m.reserved[handle] {
			continue
		}
		return handle
	}
}

func (m *AsyncCommandManager) Promote(uc *UntrackedCmd, handle string) (*AsyncCmdInfo, error) {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		uc.Kill()
		<-uc.Done()
		return nil, errors.New("async command manager is closing")
	}
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

func (m *AsyncCommandManager) Running() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	states := make([]*asyncCmdState, 0, len(m.commands))
	for _, state := range m.commands {
		states = append(states, state)
	}
	m.mu.Unlock()
	for _, state := range states {
		state.mu.Lock()
		running := state.info.Status == AsyncRunning
		state.mu.Unlock()
		if running {
			return true
		}
	}
	return false
}

// CloseAndWait prevents command processes from surviving their owning session.
func (m *AsyncCommandManager) CloseAndWait() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()
	m.startWg.Wait()
	m.mu.Lock()
	states := make([]*asyncCmdState, 0, len(m.commands))
	for _, state := range m.commands {
		states = append(states, state)
	}
	m.mu.Unlock()
	for _, state := range states {
		state.killGroup()
	}
	for _, state := range states {
		<-state.done
	}
}
