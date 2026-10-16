package session

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"crude/agent"
	"crude/fileio"
	"crude/tools"
	captypes "crude/types/capability"
)

// prepareCapabilityPackage installs an authorized MCP package through the
// owning agent's background command manager, the same path as any other
// command: it continues inline when quick, and a slow download is promoted to
// an ordinary tracked command, shown in the background jobs panel, stoppable
// by the human or the agent, and reported to the agent when it settles. There
// is no deadline; a stalled download is visible and cancellable instead of
// being killed mid-write (which left npm's cache half-populated).
//
// prepared reports whether the caller may launch now; otherwise outcome is the
// tool result to return.
func (s *Session) prepareCapabilityPackage(ctx context.Context, live *agent.Agent, request captypes.PreparationRequired) (prepared bool, outcome tools.ToolResult, err error) {
	manager := live.AsyncMgr
	if manager == nil {
		return false, tools.ToolResult{}, errors.New("MCP package preparation needs this agent's background command system")
	}
	command, err := preparationCommandLine(request.Command, request.Args)
	if err != nil {
		return false, tools.ToolResult{}, err
	}
	handle := preparationHandle(request.ConnectionID)
	if info, exists := manager.Check(handle); exists {
		switch {
		case info.Status == fileio.AsyncRunning:
			return false, tools.ToolResult{Content: preparationRunning(request, *info)}, nil
		case info.Status == fileio.AsyncCompleted && info.Command == command:
			manager.Cleanup(handle)
			return true, tools.ToolResult{}, nil
		default:
			// This activation retries a failed, stopped or different attempt.
			manager.Cleanup(handle)
		}
	}
	started, err := manager.StartUntracked(command, live.WorkDir, 0)
	if err != nil {
		return false, tools.ToolResult{}, fmt.Errorf("start MCP package preparation: %w", err)
	}
	s.logger.Info("CAPABILITY", "package_prepare_start", "connection_id", request.ConnectionID, "pid", started.PID())
	select {
	case <-ctx.Done():
		started.Kill()
		<-started.Done()
		return false, tools.ToolResult{}, ctx.Err()
	case <-started.Done():
	case <-time.After(asyncPromotionThreshold):
		if started.Status() == fileio.AsyncRunning {
			if info, promoteErr := manager.Promote(started, handle); promoteErr == nil {
				return false, tools.ToolResult{Content: preparationRunning(request, *info)}, nil
			}
		}
		select {
		case <-ctx.Done():
			started.Kill()
			<-started.Done()
			return false, tools.ToolResult{}, ctx.Err()
		case <-started.Done():
		}
	}
	if started.Info().Status == fileio.AsyncCompleted {
		return true, tools.ToolResult{}, nil
	}
	result, _ := commandOutcome(manager, started)
	return false, tools.ToolResult{Content: fmt.Sprintf(
		"Preparing the MCP package for %s failed, so the server was not launched. Diagnose the output below (for example a corrupted package cache or a network failure), repair the cause, then call capability_activate with the same id again.\n\nCommand: %s\n%s",
		request.Name, command, result.Content)}, errors.New("MCP package preparation failed")
}

func preparationRunning(request captypes.PreparationRequired, info fileio.AsyncCmdInfo) string {
	return fmt.Sprintf("The MCP package for %s is still being prepared in the background as command %q (shown in the background jobs panel). "+
		"A command_outcome event is delivered automatically when it settles; then call capability_activate with the same id to launch the server. "+
		"Meanwhile continue other work or end the turn instead of polling. read_async_output(handle=%q) shows its progress and kill_async_command(handle=%q) cancels it. %s",
		request.Name, info.Handle, info.Handle, info.Handle, FormatCommandStatus(info))
}

// preparationHandle is stable per connection so a repeated activation finds
// the in-flight preparation rather than starting a duplicate download.
func preparationHandle(connectionID string) string {
	var name strings.Builder
	for _, r := range connectionID {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			name.WriteRune(r)
		}
	}
	suffix := name.String()
	if len(suffix) > 48 {
		suffix = suffix[len(suffix)-48:]
	}
	return "mcp_prepare_" + suffix
}

// shellUnsafe reports a rune a POSIX shell could interpret in a bare word.
func shellUnsafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	default:
		return !strings.ContainsRune("@%+=:,./_-", r)
	}
}

// preparationCommandLine quotes an exact argument vector for the shell that
// background commands run under. Arguments that cmd.exe batch execution would
// reinterpret even inside quotes are refused rather than guessed.
func preparationCommandLine(command string, args []string) (string, error) {
	parts := append([]string{command}, args...)
	quoted := make([]string, len(parts))
	for index, part := range parts {
		if strings.ContainsFunc(part, func(r rune) bool { return r < ' ' || r == 0x7f }) {
			return "", fmt.Errorf("MCP package preparation argument %q contains control characters", part)
		}
		if runtime.GOOS == "windows" {
			if strings.ContainsAny(part, "\"%") {
				return "", fmt.Errorf("MCP package preparation argument %q cannot be passed through cmd.exe safely", part)
			}
			if part == "" || strings.ContainsAny(part, " \t&|<>^()") {
				part = `"` + part + `"`
			}
		} else if part == "" || strings.ContainsFunc(part, shellUnsafe) {
			part = "'" + strings.ReplaceAll(part, "'", `'\''`) + "'"
		}
		quoted[index] = part
	}
	return strings.Join(quoted, " "), nil
}
