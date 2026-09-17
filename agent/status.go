package agent

import (
	"fmt"
	"strings"
	"time"

	"crude/fileio"
)

func BuildSubAgentStatus(mgr *SubAgentManager) string {
	if mgr == nil {
		return ""
	}
	entries := mgr.ListStatus(false)
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Sub-Agents\n")
	for _, e := range entries {
		marker := "→"
		switch e.Status {
		case "completed":
			marker = "✓"
		case "failed_timeout", "failed_error", "failed_policy", "failed_quota":
			marker = "✗"
		case "cancelled":
			marker = "!"
		}
		line := fmt.Sprintf("  %s [%s:%s]: %s", marker, e.Category, e.Name, e.Status)
		if e.ElapsedSeconds > 0 {
			line += fmt.Sprintf(" (%ds, %d steps)", e.ElapsedSeconds, e.StepsUsed)
		}
		sb.WriteString(line + "\n")
	}
	return strings.TrimSpace(sb.String())
}

func BuildAsyncCommandStatus(mgr *fileio.AsyncCommandManager) string {
	commands := mgr.List()
	if len(commands) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Async Commands\n")
	shown := 0
	for _, c := range commands {
		if c.Status != fileio.AsyncRunning && time.Since(c.EndedAt) > 60*time.Second {
			continue
		}
		shown++
		marker := "?"
		switch c.Status {
		case fileio.AsyncRunning:
			marker = "→"
		case fileio.AsyncCompleted:
			marker = "✓"
		case fileio.AsyncFailed:
			marker = "✗"
		case fileio.AsyncKilled:
			marker = "!"
		}
		dur := c.Duration.Round(time.Second).String()
		fmt.Fprintf(&sb, "  %s [%s]: %s (PID %d, %s)", marker, c.Handle, c.Status, c.PID, dur)
		if c.Status == fileio.AsyncFailed && c.Error != "" {
			sb.WriteString(" — " + c.Error)
		}
		sb.WriteString("\n")
		if tail := mgr.OutputTail(c.Handle, 250); tail != "" {
			lines := strings.Split(strings.TrimRight(tail, "\n"), "\n")
			for _, line := range lines {
				sb.WriteString("    | " + line + "\n")
			}
		}
	}
	if shown == 0 {
		return ""
	}
	return strings.TrimSpace(sb.String())
}
