package agent

import (
	"fmt"
	"strings"
	"time"

	"crude/fileio"
	"crude/statemachine"
)

func BuildStateMachineStatus(sm *statemachine.ConversationStateMachine, sessionID string) string {
	if sm == nil {
		return ""
	}

	state := sm.GetState(sessionID)
	if state == nil {
		return ""
	}

	focus := sm.GetFocusGoal(sessionID)
	if focus == nil && len(state.ThreadRoots) == 0 {
		return ""
	}

	var sb strings.Builder

	if focus != nil {
		statusLabel := goalStatusLabel(focus.Status)
		fmt.Fprintf(&sb, "Goal: %s (%s)\n", focus.Intent, statusLabel)
		if focus.OriginalQuery != "" {
			fmt.Fprintf(&sb, "  request: %s\n", focus.OriginalQuery)
		}
		if focus.CurrentBlocker != nil {
			fmt.Fprintf(&sb, "  blocker [%s]: %s\n", focus.CurrentBlocker.Type.String(), focus.CurrentBlocker.Message)
		}
	}

	suspended := sm.GetSuspendedGoals(sessionID)
	if len(suspended) > 0 {
		sb.WriteString("Suspended:\n")
		for _, g := range suspended {
			fmt.Fprintf(&sb, "  %s (%s)\n", g.Intent, goalStatusLabel(g.Status))
		}
	}

	return strings.TrimSpace(sb.String())
}

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

func goalStatusLabel(s statemachine.GoalStatus) string {
	switch s {
	case statemachine.GoalPending:
		return "pending"
	case statemachine.GoalActive:
		return "active"
	case statemachine.GoalBlocked:
		return "blocked"
	case statemachine.GoalCompleted:
		return "completed"
	case statemachine.GoalFailed:
		return "failed"
	default:
		return "unknown"
	}
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
