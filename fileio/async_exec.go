package fileio

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"crude/logger"
)

type AsyncStatus string

const (
	AsyncRunning   AsyncStatus = "running"
	AsyncCompleted AsyncStatus = "completed"
	AsyncFailed    AsyncStatus = "failed"
	AsyncKilled    AsyncStatus = "killed"

	// DefaultOutputChunkBytes bounds one model-visible async output read.
	// Commands retain their complete captured output; callers page explicitly.
	DefaultOutputChunkBytes = 32 << 10
	// MaxOutputChunkBytes prevents a single caller override from flooding the
	// provider context even when the command produced substantially more output.
	MaxOutputChunkBytes = 64 << 10
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
	// OutputBytes is the complete number of raw stdout+stderr bytes retained
	// under Handle at the instant this snapshot was taken.
	OutputBytes int
}

// OutputRange identifies one half-open raw byte range included in a bounded
// command result. Multiple ranges mean the middle was omitted from the model-
// visible excerpt; the complete bytes remain addressable through Handle.
type OutputRange struct {
	StartByte int `json:"start_byte"`
	EndByte   int `json:"end_byte"`
}

// OutputDescriptor is the shared, presentation-neutral contract describing a
// bounded command result and the retained bytes behind it.
type OutputDescriptor struct {
	Handle         string        `json:"handle,omitempty"`
	Status         AsyncStatus   `json:"status"`
	TotalBytes     int           `json:"total_bytes"`
	ShownRanges    []OutputRange `json:"shown_ranges,omitempty"`
	OmittedBytes   int           `json:"omitted_bytes,omitempty"`
	RetentionError string        `json:"retention_error,omitempty"`
}

// OutputPage is one contiguous bounded read from retained command output.
type OutputPage struct {
	Descriptor OutputDescriptor `json:"descriptor"`
	SinceByte  int              `json:"since_byte"`
	NextByte   int              `json:"next_byte"`
	HasMore    bool             `json:"has_more"`
	// OutputEncoding is omitted for ordinary UTF-8 text. A base64 value means
	// Output encodes the exact raw ShownRanges bytes because the requested byte
	// interval was not independently valid UTF-8 (for example, a mid-rune cursor
	// or binary command output).
	OutputEncoding string `json:"output_encoding,omitempty"`
	Output         string `json:"output"`
}

type AsyncCmdRef struct {
	Handle    string
	PID       int
	StartedAt time.Time
}

var (
	ErrAsyncCommandNotFound   = errors.New("async command not found")
	ErrAsyncCommandNotRunning = errors.New("async command is no longer running")
	ErrAsyncCommandReplaced   = errors.New("async command identity no longer matches")
)

func (info AsyncCmdInfo) Ref() AsyncCmdRef {
	return AsyncCmdRef{Handle: info.Handle, PID: info.PID, StartedAt: info.StartedAt}
}

type ownedCommandTree struct {
	wait  func() error
	kill  func() error
	close func()
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
	readCursor       int
	tree             *ownedCommandTree
}

func (st *asyncCmdState) killGroup() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.info.Status != AsyncRunning || st.cmd == nil || st.cmd.Process == nil {
		return
	}
	st.cancel()
	if st.tree != nil {
		_ = st.tree.kill()
	} else {
		_ = cancelProcess(st.cmd)
	}
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
	tempDir     string
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

// SetTempDir configures the cross-platform transient directory advertised to
// agents and inherited by their commands as CRUDE_TEMP_DIR.
func (m *AsyncCommandManager) SetTempDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("command temp directory is empty")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create command temp directory: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.commands) > 0 || len(m.reserved) > 0 {
		return errors.New("cannot change command temp directory after commands started")
	}
	m.tempDir = dir
	return nil
}

func (m *AsyncCommandManager) TempDir() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tempDir
}

func commandEnvironmentWithTemp(existing []string, tempDir string) []string {
	if strings.TrimSpace(tempDir) == "" {
		return existing
	}
	environment := existing
	if environment == nil {
		environment = os.Environ()
	} else {
		environment = append([]string(nil), environment...)
	}
	const key = "CRUDE_TEMP_DIR"
	replacement := key + "=" + tempDir
	for index, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, key) {
			environment[index] = replacement
			return environment
		}
	}
	return append(environment, replacement)
}

func buildCommand(ctx context.Context, command string, cwd string, tempDir string) (*exec.Cmd, func(), error) {
	if runtime.GOOS == "windows" {
		tmp, err := os.CreateTemp(tempDir, "crude_*.cmd")
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
		c.Env = commandEnvironmentWithTemp(c.Env, tempDir)
		return c, func() { _ = os.Remove(batPath) }, nil
	}

	shell, args := shellForExec()
	c := setProcessGroupCmd(ctx, shell, append(args, command)...)
	if cwd != "" {
		c.Dir = cwd
	}
	c.Env = commandEnvironmentWithTemp(c.Env, tempDir)
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
	tempDir := m.tempDir
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

	c, cleanup, err := buildCommand(ctx, command, cwd, tempDir)
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
	c.Cancel = func() error { state.killGroup(); return nil }

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
	tree, err := ownProcessTree(c)
	if err != nil {
		state.killGroup()
		_ = c.Wait()
		cleanup()
		releaseReservation()
		return nil, err
	}
	state.mu.Lock()
	state.tree = tree
	state.mu.Unlock()
	state.cleanup = func() { tree.close(); cleanup() }
	if err := resumeOwnedProcess(c); err != nil {
		state.killGroup()
		_ = c.Wait()
		state.cleanup()
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
		err := tree.wait()
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
	state.outputMu.Lock()
	info.OutputBytes = state.output.Len()
	state.outputMu.Unlock()
	info.computeDuration()
	if m.logger != nil {
		m.logger.Debug("ASYNC", "check", "handle", handle, "status", info.Status, "pid", info.PID)
	}
	return &info, true
}

func (m *AsyncCommandManager) Kill(handle string) error {
	info, ok := m.Check(handle)
	if !ok {
		return fmt.Errorf("%w: %q", ErrAsyncCommandNotFound, handle)
	}
	if info.Status != AsyncRunning {
		return nil
	}
	return m.KillRef(info.Ref())
}

func (m *AsyncCommandManager) KillRef(ref AsyncCmdRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.commands[ref.Handle]
	if !ok {
		return fmt.Errorf("%w: %q", ErrAsyncCommandNotFound, ref.Handle)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if ref.PID <= 0 || ref.StartedAt.IsZero() || state.info.PID != ref.PID || !state.info.StartedAt.Equal(ref.StartedAt) {
		return fmt.Errorf("%w: %q", ErrAsyncCommandReplaced, ref.Handle)
	}
	if state.info.Status != AsyncRunning || state.tree == nil {
		return fmt.Errorf("%w: %q", ErrAsyncCommandNotRunning, ref.Handle)
	}
	if m.logger != nil {
		m.logger.Info("ASYNC", "kill", "handle", ref.Handle, "pid", ref.PID)
	}
	state.cancel()
	if err := state.tree.kill(); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("%w: %q", ErrAsyncCommandNotRunning, ref.Handle)
		}
		return err
	}
	return nil
}

func normalizeOutputLimit(maxBytes int) int {
	if maxBytes <= 0 {
		return DefaultOutputChunkBytes
	}
	return min(maxBytes, MaxOutputChunkBytes)
}

func normalizeCommandOutput(out string) string {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	return strings.ReplaceAll(out, "\r", "")
}

// ReadOutput reads one bounded output page. nextByte advances only through the
// returned raw byte range; totalBytes reports the complete captured size.
func (m *AsyncCommandManager) ReadOutputPage(handle string, sinceByte, maxBytes int) (OutputPage, error) {
	m.mu.Lock()
	state, ok := m.commands[handle]
	m.mu.Unlock()
	if !ok {
		if m.logger != nil {
			m.logger.Warn("ASYNC", "read_output_not_found", "handle", handle)
		}
		return OutputPage{}, fmt.Errorf("async command handle %q not found", handle)
	}
	if sinceByte < 0 {
		return OutputPage{}, errors.New("async output cursor cannot be negative")
	}
	maxBytes = normalizeOutputLimit(maxBytes)
	state.outputMu.Lock()
	total := state.output.Len()
	end := min(total, sinceByte+maxBytes)
	if sinceByte > total {
		end = total
	}
	var raw []byte
	if sinceByte >= total {
		state.readCursor = max(state.readCursor, total)
	} else {
		raw = append([]byte(nil), state.output.Bytes()[sinceByte:end]...)
		state.readCursor = max(state.readCursor, end)
	}
	state.outputMu.Unlock()
	var out, encoding string
	if utf8.Valid(raw) {
		out = normalizeCommandOutput(string(raw))
	} else {
		out = base64.StdEncoding.EncodeToString(raw)
		encoding = "base64"
	}
	state.mu.Lock()
	status := state.info.Status
	state.mu.Unlock()
	descriptor := OutputDescriptor{Handle: handle, Status: status, TotalBytes: total}
	if end > sinceByte && sinceByte < total {
		descriptor.ShownRanges = []OutputRange{{StartByte: sinceByte, EndByte: end}}
	}
	page := OutputPage{Descriptor: descriptor, SinceByte: sinceByte, NextByte: end, HasMore: end < total, OutputEncoding: encoding, Output: out}
	if m.logger != nil {
		m.logger.Debug("ASYNC", "read_output", "handle", handle, "bytes", len(out), "since_byte", sinceByte, "total", total, "next_byte", end, "status", status)
	}
	return page, nil

}

func (m *AsyncCommandManager) ReadOutputLimit(handle string, sinceByte, maxBytes int) (out string, nextByte, totalBytes int, err error) {
	page, err := m.ReadOutputPage(handle, sinceByte, maxBytes)
	if err != nil {
		return "", 0, 0, err
	}
	return page.Output, page.NextByte, page.Descriptor.TotalBytes, nil
}

func (m *AsyncCommandManager) ReadOutput(handle string, sinceByte int) (string, int, error) {
	out, next, _, err := m.ReadOutputLimit(handle, sinceByte, DefaultOutputChunkBytes)
	return out, next, err
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
	return normalizeCommandOutput(string(state.output.Bytes()[start:n]))
}

func (m *AsyncCommandManager) List() []AsyncCmdInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]AsyncCmdInfo, 0, len(m.commands))
	for _, state := range m.commands {
		state.mu.Lock()
		info := state.info
		state.mu.Unlock()
		state.outputMu.Lock()
		info.OutputBytes = state.output.Len()
		state.outputMu.Unlock()
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
	tempDir := m.tempDir
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
		c, cleanup, err = buildCommand(ctx, command, cwd, tempDir)
	} else {
		c, cleanup, err = buildInteractiveCommand(ctx, *interactive, tempDir)
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
	c.Cancel = func() error { state.killGroup(); return nil }

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
	tree, err := ownProcessTree(c)
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
	state.mu.Lock()
	state.tree = tree
	state.mu.Unlock()
	state.cleanup = func() {
		if state.input != nil {
			_ = state.input.Close()
		}
		tree.close()
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
		err := tree.wait()
		if outputDone != nil {
			// Closing a PTY after process exit is safe after its final output drains.
			select {
			case <-outputDone:
			case <-time.After(time.Second):
				_ = state.input.Close()
				<-outputDone
			}
		}
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

func (uc *UntrackedCmd) ReadOutputLimit(sinceByte, maxBytes int) (out string, nextByte, totalBytes int) {
	if sinceByte < 0 {
		sinceByte = 0
	}
	maxBytes = normalizeOutputLimit(maxBytes)
	uc.state.outputMu.Lock()
	total := uc.state.output.Len()
	if sinceByte >= total {
		uc.state.outputMu.Unlock()
		return "", total, total
	}
	end := min(total, sinceByte+maxBytes)
	out = string(uc.state.output.Bytes()[sinceByte:end])
	uc.state.outputMu.Unlock()
	return normalizeCommandOutput(out), end, total
}

func (uc *UntrackedCmd) ReadOutput(sinceByte int) (string, int) {
	out, next, _ := uc.ReadOutputLimit(sinceByte, DefaultOutputChunkBytes)
	return out, next
}

func (uc *UntrackedCmd) Info() AsyncCmdInfo {
	uc.state.mu.Lock()
	info := uc.state.info
	uc.state.mu.Unlock()
	uc.state.outputMu.Lock()
	info.OutputBytes = uc.state.output.Len()
	uc.state.outputMu.Unlock()
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
	uc.state.outputMu.Lock()
	info.OutputBytes = uc.state.output.Len()
	uc.state.outputMu.Unlock()
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
