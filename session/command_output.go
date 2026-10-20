package session

import (
	"context"
	"fmt"
	"time"

	"crude/fileio"
	"crude/logger"
	"crude/tools"
	"crude/types/command"
)

func commandResult(info fileio.AsyncCmdInfo) command.Result {
	result := command.Result{Command: info.Command, CWD: info.CWD, PID: info.PID, Error: info.Error,
		WallTimeSeconds:  info.Duration.Seconds(),
		OutputDescriptor: command.OutputDescriptor{Handle: info.Handle, Status: info.Status, TotalBytes: info.OutputBytes}}
	if info.Status == fileio.AsyncCompleted || info.Status == fileio.AsyncFailed {
		code := info.ExitCode
		result.ExitCode = &code
	}
	return result
}

func FormatCommandStart(info fileio.AsyncCmdInfo) string {
	result := commandResult(info)
	result.Retrieval = fmt.Sprintf("Command promoted to async command [%s]. Read content with read_async_output(handle=%q, since_byte=0, max_bytes=%d); use check_async_command only for lifecycle status and kill_async_command(handle=%q) to stop this exact command. Its completion, failure or timeout is delivered automatically as a command_outcome event. Do not rerun the command or repeat status checks for content.", info.Handle, info.Handle, fileio.DefaultOutputChunkBytes, info.Handle)
	return tools.CommandResultContent(result)
}

func FormatCommandStatus(info fileio.AsyncCmdInfo) string {
	result := commandResult(info)
	result.Retrieval = fmt.Sprintf("Read retained content with read_async_output(handle=%q, since_byte=0, max_bytes=%d); do not rerun the command for output.", info.Handle, fileio.DefaultOutputChunkBytes)
	if info.Status == fileio.AsyncRunning {
		result.Retrieval += " Exit unknown while running; do not repeat status checks for content."
	}
	return tools.CommandResultContent(result)
}

func FormatCommandPage(page fileio.OutputPage) string {
	result := command.Result{Command: page.Command, CWD: page.CWD, OutputDescriptor: page.Descriptor,
		Output: page.Output, OutputEncoding: page.OutputEncoding,
		Page: &command.Page{SinceByte: page.SinceByte, NextByte: page.NextByte, HasMore: page.HasMore}}
	if page.OutputEncoding == "base64" {
		result.Retrieval = "Decode this returned page to recover the exact raw bytes; continue from next byte rather than rereading this cursor. "
	}
	if page.HasMore {
		result.Retrieval += fmt.Sprintf("More output remains. Continue with since_byte=%d (max_bytes=%d); do not rerun the command or status-check for content.", page.NextByte, fileio.DefaultOutputChunkBytes)
	}
	if page.Output == "" {
		if page.Descriptor.Status == fileio.AsyncRunning {
			result.Retrieval += "No new bytes yet; the command is still running, so wait for natural progress before one later read."
		} else {
			result.Retrieval += "Output is complete; no bytes exist in this range."
		}
	}
	return tools.CommandResultContent(result)
}

// commandOutcome renders the terminal state of an untracked command as a tool
// result, preserving partial output on failure.
func commandOutcome(mgr *fileio.AsyncCommandManager, uc *fileio.UntrackedCmd) (tools.ToolResult, error) {
	info := uc.Info()
	result := uc.OutputResult()
	if result.OutputDescriptor.OmittedBytes > 0 {
		retainedHandle := mgr.NextAutoHandle()
		if _, err := mgr.RetainSettled(uc, retainedHandle); err != nil {
			result.OutputDescriptor.RetentionError = err.Error()
		} else {
			result.OutputDescriptor.Handle = retainedHandle
			result.Retrieval = fmt.Sprintf("First omitted cursor: read_async_output(handle=%q, since_byte=%d, max_bytes=%d). Do not rerun the command or status-check for content.", retainedHandle, result.OutputDescriptor.ShownRanges[0].EndByte, fileio.DefaultOutputChunkBytes)
		}
	}
	if info.Status == fileio.AsyncCompleted {
		return tools.ToolResult{Content: tools.CommandResultContent(result)}, nil
	}
	errMsg := info.Error
	if errMsg == "" {
		errMsg = fmt.Sprintf("exit code %d", info.ExitCode)
	}
	return tools.ToolResult{Content: tools.CommandResultContent(result)}, fmt.Errorf("command failed: %s", errMsg)
}

func cancelledCommandOutcome(mgr *fileio.AsyncCommandManager, uc *fileio.UntrackedCmd) tools.ToolResult {
	result, _ := commandOutcome(mgr, uc)
	if evidence, ok := tools.ParseCommandResult("execute_command", result.Content); ok {
		evidence.Cancelled = true
		evidence.Error = "Command cancelled. The owning turn stopped its exact command tree and preserved the captured evidence."
		result.Content = tools.CommandResultContent(evidence)
	}
	return result
}

// awaitCommandWithPromotion waits for a command started via StartUntracked.
// A command still running after asyncPromotionThreshold is registered with mgr
// under a fresh auto handle so the turn can continue instead of blocking; the
// result then tells the model how to check, read, or kill it. agentID is only
// used for log attribution and is empty for session-level commands.
func awaitCommandWithPromotion(
	ctx context.Context,
	mgr *fileio.AsyncCommandManager,
	uc *fileio.UntrackedCmd,
	lg *logger.Logger,
	agentID string,
) (tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		uc.Kill()
		<-uc.Done()
		return cancelledCommandOutcome(mgr, uc), nil
	case <-uc.Done():
		return commandOutcome(mgr, uc)
	case <-time.After(asyncPromotionThreshold):
	}

	if uc.Status() == fileio.AsyncRunning {
		handle := mgr.NextAutoHandle()
		if _, err := mgr.Promote(uc, handle); err == nil {
			if lg != nil {
				fields := []any{"handle", handle, "pid", uc.PID(), "threshold", asyncPromotionThreshold.String()}
				if agentID != "" {
					fields = append(fields, "agent_id", agentID)
				}
				lg.Info("ASYNC", "auto_promote", fields...)
			}
			info := uc.Info()
			info.Handle = handle
			return tools.ToolResult{Content: FormatCommandStart(info)}, nil
		} else if lg != nil {
			lg.Error("ASYNC", "auto_promote_failed", "handle", handle, "error", err.Error())
		}
	}

	// The command finished inside the promotion window, or promotion failed:
	// fall back to the blocking path rather than losing the command.
	select {
	case <-ctx.Done():
		uc.Kill()
		<-uc.Done()
		return cancelledCommandOutcome(mgr, uc), nil
	case <-uc.Done():
		return commandOutcome(mgr, uc)
	}
}
