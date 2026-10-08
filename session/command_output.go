package session

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"crude/fileio"
	"crude/logger"
	"crude/tools"
)

func trimIncompleteUTF8End(s string) (string, int) {
	removed := 0
	for s != "" && !utf8.ValidString(s) && removed < utf8.UTFMax-1 {
		s = s[:len(s)-1]
		removed++
	}
	return s, removed
}

func trimIncompleteUTF8Start(s string) (string, int) {
	removed := 0
	for s != "" && !utf8.ValidString(s) && removed < utf8.UTFMax-1 {
		s = s[1:]
		removed++
	}
	return s, removed
}

func FormatCommandStart(info fileio.AsyncCmdInfo) string {
	return fmt.Sprintf("Command promoted to async command [%s] (PID %d): status=%s, retained_bytes=%d, exit=unknown. Read content with read_async_output(handle=%q, since_byte=0, max_bytes=%d); use check_async_command only for lifecycle status and kill_async_command(handle=%q) to stop this exact command. Do not rerun the command or repeat status checks for content.", info.Handle, info.PID, info.Status, info.OutputBytes, info.Handle, fileio.DefaultOutputChunkBytes, info.Handle)
}

func FormatCommandStatus(info fileio.AsyncCmdInfo) string {
	base := fmt.Sprintf("handle=%s status=%s pid=%d output_bytes=%d", info.Handle, info.Status, info.PID, info.OutputBytes)
	if info.Status == fileio.AsyncRunning {
		return base + fmt.Sprintf("; exit unknown while running. Read content with read_async_output(handle=%q, since_byte=0, max_bytes=%d), not repeated status checks.", info.Handle, fileio.DefaultOutputChunkBytes)
	}
	if info.Status == fileio.AsyncKilled {
		base += "; exit unavailable (command was killed)"
	} else {
		base += fmt.Sprintf(" exit_code=%d", info.ExitCode)
	}
	if info.Error != "" {
		base += fmt.Sprintf(" error=%q", info.Error)
	}
	return base + fmt.Sprintf(". Read retained content with read_async_output(handle=%q, since_byte=0, max_bytes=%d); do not rerun the command for output.", info.Handle, fileio.DefaultOutputChunkBytes)
}

func FormatCommandPage(page fileio.OutputPage) string {
	header := fmt.Sprintf("handle=%s status=%s bytes=%d-%d of %d", page.Descriptor.Handle, page.Descriptor.Status, page.SinceByte, page.NextByte, page.Descriptor.TotalBytes)
	if page.OutputEncoding == "base64" {
		header += "; output_encoding=base64 (decode this returned page to recover the exact raw bytes; continue from next byte rather than rereading this cursor)"
	}
	if page.HasMore {
		header += fmt.Sprintf("; more output remains. Continue with since_byte=%d (max_bytes=%d); do not rerun the command or status-check for content.", page.NextByte, fileio.DefaultOutputChunkBytes)
	}
	if page.Output == "" {
		if page.Descriptor.Status == fileio.AsyncRunning {
			return header + "\n\n(no new bytes yet; the command is still running, so wait for natural progress before one later read)"
		}
		return header + "\n\n(output is complete; no bytes exist in this range)"
	}
	return header + "\n\n" + page.Output
}

func formatOutputHeader(descriptor fileio.OutputDescriptor, info fileio.AsyncCmdInfo) string {
	header := fmt.Sprintf("Command output: status=%s total_bytes=%d", descriptor.Status, descriptor.TotalBytes)
	if descriptor.Handle != "" {
		header += fmt.Sprintf(" retained_handle=%s", descriptor.Handle)
	} else {
		header += " retained_handle=none"
	}
	switch descriptor.Status {
	case fileio.AsyncRunning:
		header += " exit=unknown"
	case fileio.AsyncKilled:
		header += " exit=unavailable"
	default:
		header += fmt.Sprintf(" exit_code=%d", info.ExitCode)
	}
	if len(descriptor.ShownRanges) == 0 {
		header += " shown_ranges=none"
	} else {
		header += " shown_ranges="
		for i, r := range descriptor.ShownRanges {
			if i > 0 {
				header += ","
			}
			header += fmt.Sprintf("%d-%d", r.StartByte, r.EndByte)
		}
	}
	header += fmt.Sprintf(" omitted_bytes=%d", descriptor.OmittedBytes)
	if descriptor.RetentionError != "" {
		header += fmt.Sprintf(" retention_error=%q", descriptor.RetentionError)
	}
	return header
}

// commandOutcome renders the terminal state of an untracked command as a tool
// result, preserving partial output on failure.
func commandOutcome(mgr *fileio.AsyncCommandManager, uc *fileio.UntrackedCmd) (tools.ToolResult, error) {
	const excerptBytes = fileio.DefaultOutputChunkBytes / 2
	head, headEnd, total := uc.ReadOutputLimit(0, excerptBytes)
	var trimmed int
	head, trimmed = trimIncompleteUTF8End(head)
	headEnd -= trimmed
	info := uc.Info()
	out := head
	descriptor := fileio.OutputDescriptor{Status: info.Status, TotalBytes: total}
	if head != "" {
		descriptor.ShownRanges = append(descriptor.ShownRanges, fileio.OutputRange{StartByte: 0, EndByte: headEnd})
	}
	if total > excerptBytes {
		tailStart := excerptBytes
		if total > excerptBytes*2 {
			tailStart = total - excerptBytes
		}
		tail, tailEnd, _ := uc.ReadOutputLimit(tailStart, excerptBytes)
		tail, trimmed = trimIncompleteUTF8Start(tail)
		tailStart += trimmed
		descriptor.ShownRanges = append(descriptor.ShownRanges, fileio.OutputRange{StartByte: tailStart, EndByte: tailEnd})
		if omitted := tailStart - headEnd; omitted > 0 {
			descriptor.OmittedBytes = omitted
			out += fmt.Sprintf("\n\n... %d output bytes omitted ...\n\n", omitted)
		}
		out += tail
		if total > excerptBytes*2 {
			retainedHandle := mgr.NextAutoHandle()
			if _, err := mgr.Promote(uc, retainedHandle); err != nil {
				descriptor.RetentionError = err.Error()
				out += "\n\n[Full output could not be retained as a command handle: " + err.Error() + "]"
			} else {
				descriptor.Handle = retainedHandle
			}
		}
	}
	if descriptor.Handle == "" && descriptor.RetentionError == "" && descriptor.OmittedBytes == 0 && total > 0 {
		descriptor.ShownRanges = []fileio.OutputRange{{StartByte: 0, EndByte: total}}
	}
	header := formatOutputHeader(descriptor, info)
	if descriptor.Handle != "" {
		header += fmt.Sprintf(". First omitted cursor: read_async_output(handle=%q, since_byte=%d, max_bytes=%d). Do not rerun the command or status-check for content.", descriptor.Handle, headEnd, fileio.DefaultOutputChunkBytes)
	}
	if info.Status == fileio.AsyncCompleted {
		if out == "" {
			return tools.ToolResult{Content: header + "\n\n(command completed with no output)"}, nil
		}
		return tools.ToolResult{Content: header + "\n\n" + out}, nil
	}
	errMsg := info.Error
	if errMsg == "" {
		errMsg = fmt.Sprintf("exit code %d", info.ExitCode)
	}
	label := "Command failed"
	if info.Status == fileio.AsyncKilled {
		label = "Command stopped"
	}
	content := header + fmt.Sprintf("\n\n%s: %s", label, errMsg)
	if out != "" {
		content += "\n\n" + out
	}
	return tools.ToolResult{Content: content}, fmt.Errorf("%s", content)
}

func cancelledCommandOutcome(mgr *fileio.AsyncCommandManager, uc *fileio.UntrackedCmd) tools.ToolResult {
	result, _ := commandOutcome(mgr, uc)
	result.Content = "Command cancelled. The owning turn stopped its exact command tree and preserved the captured evidence below.\n" + result.Content
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
			return tools.ToolResult{Content: fmt.Sprintf("Command still running after %s. %s", asyncPromotionThreshold, FormatCommandStart(info))}, nil
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
