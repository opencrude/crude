package fileio

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// InteractiveOptions configures Codex's exec_command on the existing command
// manager. Its process ownership and shutdown authority are shared with all jobs.
type InteractiveOptions struct {
	Command, CWD, Shell string
	Login, TTY          bool
}

type InteractiveResult struct {
	WallTimeSeconds    float64 `json:"wall_time_seconds"`
	ExitCode           *int    `json:"exit_code,omitempty"`
	SessionID          int     `json:"session_id,omitempty"`
	OriginalTokenCount int     `json:"original_token_count,omitempty"`
	Output             string  `json:"output"`
}

func buildInteractiveCommand(ctx context.Context, opts InteractiveOptions, tempDir string) (*exec.Cmd, func(), error) {
	shell := opts.Shell
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		if runtime.GOOS == "windows" {
			shell = "powershell.exe"
		} else {
			shell = "/bin/sh"
		}
	}
	var args []string
	switch strings.ToLower(strings.TrimSuffix(filepath.Base(shell), ".exe")) {
	case "cmd":
		args = []string{"/D", "/S", "/C", opts.Command}
	case "powershell", "pwsh":
		args = []string{"-Command", opts.Command}
		if !opts.Login {
			args = append([]string{"-NoProfile"}, args...)
		}
	default:
		flag := "-c"
		if opts.Login {
			flag = "-lc"
		}
		args = []string{flag, opts.Command}
	}
	cmd := setProcessGroupCmd(ctx, shell, args...)
	cmd.Dir = opts.CWD
	cmd.Env = commandEnvironmentWithTemp(cmd.Env, tempDir)
	return cmd, func() {}, nil
}

func (m *AsyncCommandManager) StartInteractive(ctx context.Context, opts InteractiveOptions, yield time.Duration, maxTokens int) (InteractiveResult, error) {
	if err := ctx.Err(); err != nil {
		return InteractiveResult{}, err
	}
	uc, err := m.startUntracked(opts.Command, opts.CWD, 0, &opts)
	if err != nil {
		return InteractiveResult{}, err
	}
	// Handles are never reused. The numeric part is the native session_id.
	handle := m.NextAutoHandle()
	id, err := strconv.Atoi(strings.TrimPrefix(handle, "auto-"))
	if err != nil {
		uc.Kill()
		return InteractiveResult{}, err
	}
	if _, err := m.Promote(uc, handle); err != nil {
		uc.Kill()
		return InteractiveResult{}, err
	}
	return m.interact(ctx, id, uc.state, "", yield, maxTokens)
}

func (m *AsyncCommandManager) WriteInteractive(ctx context.Context, id int, chars string, yield time.Duration, maxTokens int) (InteractiveResult, error) {
	m.mu.Lock()
	state := m.commands[fmt.Sprintf("auto-%d", id)]
	m.mu.Unlock()
	if state == nil || state.input == nil {
		return InteractiveResult{}, fmt.Errorf("unknown exec session %d", id)
	}
	return m.interact(ctx, id, state, chars, yield, maxTokens)
}

func (m *AsyncCommandManager) interact(ctx context.Context, id int, state *asyncCmdState, chars string, yield time.Duration, maxTokens int) (InteractiveResult, error) {
	start := time.Now()
	select {
	case state.interactionLease <- struct{}{}:
		defer func() { <-state.interactionLease }()
	case <-ctx.Done():
		return InteractiveResult{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		state.killGroup()
		return InteractiveResult{}, err
	}
	if chars != "" {
		written := make(chan error, 1)
		go func() { _, err := state.input.Write([]byte(chars)); written <- err }()
		select {
		case err := <-written:
			if err != nil {
				return InteractiveResult{}, fmt.Errorf("write stdin: %w", err)
			}
		case <-ctx.Done():
			state.killGroup()
			_ = state.input.Close()
			<-written
			return InteractiveResult{}, ctx.Err()
		}
	}
	timer := time.NewTimer(yield)
	defer timer.Stop()
	select {
	case <-state.done:
	case <-timer.C:
	case <-ctx.Done():
		state.killGroup()
		return InteractiveResult{}, ctx.Err()
	}
	state.outputMu.Lock()
	output := string(state.output.Bytes()[state.outputCursor:])
	state.outputCursor = state.output.Len()
	state.outputMu.Unlock()
	result := InteractiveResult{WallTimeSeconds: time.Since(start).Seconds(), Output: output}
	tokens := (utf8.RuneCountInString(output) + 3) / 4
	if maxTokens > 0 && tokens > maxTokens {
		runes := []rune(output)
		budget := maxTokens * 4
		half := budget / 2
		result.Output = string(runes[:half]) + "\n... output truncated ...\n" + string(runes[len(runes)-(budget-half):])
		result.OriginalTokenCount = tokens
	}
	state.mu.Lock()
	info := state.info
	state.mu.Unlock()
	if info.Status == AsyncRunning {
		result.SessionID = id
	} else {
		code := info.ExitCode
		if info.Status == AsyncKilled {
			code = -1
		}
		result.ExitCode = &code
	}
	return result, nil
}
