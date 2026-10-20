package fileio

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"crude/types/command"
)

// InteractiveOptions configures Codex's exec_command on the existing command
// manager. Its process ownership and shutdown authority are shared with all jobs.
type InteractiveOptions struct {
	Command, CWD, Shell string
	Login, TTY          bool
}

type InteractiveResult = command.Result

func utf8PrefixBoundary(raw string, limit int) int {
	if limit >= len(raw) {
		return len(raw)
	}
	if limit <= 0 {
		return 0
	}
	for limit > 0 && !utf8.RuneStart(raw[limit]) {
		limit--
	}
	return limit
}

func utf8SuffixBoundary(raw string, start int) int {
	if start <= 0 {
		return 0
	}
	if start >= len(raw) {
		return len(raw)
	}
	for start < len(raw) && !utf8.RuneStart(raw[start]) {
		start++
	}
	return start
}

func interactiveRetrieval(descriptor OutputDescriptor, info AsyncCmdInfo, firstOmitted int) string {
	header := fmt.Sprintf("Command output: handle=%s status=%s total_bytes=%d", descriptor.Handle, descriptor.Status, descriptor.TotalBytes)
	switch descriptor.Status {
	case AsyncRunning:
		header += "; exit unknown while running"
	case AsyncKilled:
		header += "; exit unavailable (command was killed)"
	default:
		header += fmt.Sprintf("; exit_code=%d", info.ExitCode)
	}
	if info.Error != "" {
		header += fmt.Sprintf("; error=%q", info.Error)
	}
	if descriptor.OmittedBytes > 0 {
		header += fmt.Sprintf(". Visible output is a bounded excerpt; %d bytes are omitted. Read the first omitted bytes with read_async_output(handle=%q, since_byte=%d, max_bytes=%d); do not rerun the command or repeat status checks for content.", descriptor.OmittedBytes, descriptor.Handle, firstOmitted, DefaultOutputChunkBytes)
	} else if descriptor.Status == AsyncRunning && descriptor.TotalBytes == 0 {
		header += ". No new bytes yet; wait for natural command progress before one later read instead of looping on status."
	} else if descriptor.TotalBytes == 0 {
		header += ". Output is complete and empty; no output read is needed."
	} else {
		header += ". All newly captured bytes are shown below; retained output remains addressable by this handle without rerunning the command."
	}
	return header
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
		state.killGroup()
		<-state.done
		return interactiveResultSnapshot(id, state, start, maxTokens), ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		state.killGroup()
		<-state.done
		return interactiveResultSnapshot(id, state, start, maxTokens), err
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
			<-state.done
			return interactiveResultSnapshot(id, state, start, maxTokens), ctx.Err()
		}
	}
	timer := time.NewTimer(yield)
	defer timer.Stop()
	select {
	case <-state.done:
	case <-timer.C:
	case <-ctx.Done():
		state.killGroup()
		<-state.done
		return interactiveResultSnapshot(id, state, start, maxTokens), ctx.Err()
	}
	return interactiveResultSnapshot(id, state, start, maxTokens), nil
}

func interactiveResultSnapshot(id int, state *asyncCmdState, start time.Time, maxTokens int) InteractiveResult {
	state.outputMu.Lock()
	outputStart := state.outputCursor
	outputEnd := state.output.Len()
	output := string(state.output.Bytes()[outputStart:outputEnd])
	state.outputCursor = state.output.Len()
	state.outputMu.Unlock()
	state.mu.Lock()
	info := state.info
	state.mu.Unlock()
	if info.Status != AsyncRunning {
		state.markSettleObserved()
	}
	descriptor := OutputDescriptor{
		Handle:     fmt.Sprintf("auto-%d", id),
		Status:     info.Status,
		TotalBytes: outputEnd,
	}
	if outputEnd > outputStart {
		descriptor.ShownRanges = []OutputRange{{StartByte: outputStart, EndByte: outputEnd}}
	}
	result := InteractiveResult{Command: info.Command, CWD: info.CWD, PID: info.PID, Error: info.Error, WallTimeSeconds: time.Since(start).Seconds(), Output: output, OutputDescriptor: descriptor}
	tokens := (utf8.RuneCountInString(output) + 3) / 4
	firstOmitted := outputEnd
	byteBudget := maxTokens * 4
	if !utf8.ValidString(output) {
		shownEnd := len(output)
		if maxTokens > 0 {
			shownEnd = min(shownEnd, byteBudget)
		}
		result.Output = base64.StdEncoding.EncodeToString([]byte(output[:shownEnd]))
		result.OutputEncoding = "base64"
		result.OutputDescriptor.ShownRanges = []OutputRange{{StartByte: outputStart, EndByte: outputStart + shownEnd}}
		result.OutputDescriptor.OmittedBytes = len(output) - shownEnd
		firstOmitted = outputStart + shownEnd
	} else if maxTokens > 0 && len(output) > byteBudget {
		headEnd := utf8PrefixBoundary(output, byteBudget/2)
		tailStart := utf8SuffixBoundary(output, len(output)-(byteBudget-headEnd))
		result.Output = output[:headEnd] + "\n... output truncated ...\n" + output[tailStart:]
		result.OriginalTokenCount = tokens
		result.OutputDescriptor.ShownRanges = []OutputRange{
			{StartByte: outputStart, EndByte: outputStart + headEnd},
			{StartByte: outputStart + tailStart, EndByte: outputEnd},
		}
		result.OutputDescriptor.OmittedBytes = max(0, tailStart-headEnd)
		firstOmitted = outputStart + headEnd
	}
	if info.Status == AsyncRunning {
		result.SessionID = id
	} else if info.Status != AsyncKilled {
		code := info.ExitCode
		result.ExitCode = &code
	}
	if result.OutputEncoding == "" {
		result.Output, result.OutputEncoding = commandOutputPayload(result.Output)
	}
	result.Retrieval = interactiveRetrieval(result.OutputDescriptor, info, firstOmitted)
	if result.OutputEncoding == "base64" {
		result.Retrieval += " The output field uses output_encoding=base64; decode only the returned field to recover its exact raw shown range."
	}
	return result
}
