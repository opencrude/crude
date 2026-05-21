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
		isPlan := strings.HasPrefix(focus.Intent, "plan:")

		if isPlan {
			objective := strings.TrimPrefix(focus.Intent, "plan: ")
			maxObjRunes := 80
			objRunes := []rune(objective)
			if len(objRunes) > maxObjRunes {
				objective = string(objRunes[:maxObjRunes]) + "…"
			}
			sb.WriteString(fmt.Sprintf("Plan: %s (%s)\n", objective, statusLabel))

			rawStatuses, _ := state.GetFromContext("plan_step_statuses")
			rawLabels, _ := state.GetFromContext("plan_step_labels")
			stepStatuses, _ := rawStatuses.(map[string]string)
			stepLabels, _ := rawLabels.(map[string]string)

			if stepStatuses != nil {
				total := 0
				completed := 0
				rawSteps, _ := state.GetFromContext("plan_steps")
				if steps, ok := rawSteps.([]map[string]any); ok {
					total = len(steps)
					for _, step := range steps {
						id, _ := step["id"].(string)
						desc := stepLabels[id]
						status := stepStatuses[id]
						marker := persistentStepMarker(status)
						if status == "completed" {
							completed++
						}
						line := fmt.Sprintf("  %s %s: %s", marker, id, desc)
						sb.WriteString(line + "\n")
					}
				} else {
					for id, status := range stepStatuses {
						marker := persistentStepMarker(status)
						desc := stepLabels[id]
						line := fmt.Sprintf("  %s %s: %s", marker, id, desc)
						if status == "completed" {
							completed++
						}
						total++
						sb.WriteString(line + "\n")
					}
				}

				var summaryParts []string
				summaryParts = append(summaryParts, fmt.Sprintf("%d/%d steps", completed, total))
				if focus.CurrentBlocker != nil {
					summaryParts = append(summaryParts, fmt.Sprintf("blocker: %s", focus.CurrentBlocker.Message))
				}
				if len(summaryParts) > 0 {
					sb.WriteString(fmt.Sprintf("  %s\n", strings.Join(summaryParts, "  |  ")))
				}
			}
		} else {
			sb.WriteString(fmt.Sprintf("Goal: %s (%s)\n", focus.Intent, statusLabel))
			if focus.OriginalQuery != "" {
				sb.WriteString(fmt.Sprintf("  request: %s\n", focus.OriginalQuery))
			}
			if focus.CurrentBlocker != nil {
				sb.WriteString(fmt.Sprintf("  blocker [%s]: %s\n", focus.CurrentBlocker.Type.String(), focus.CurrentBlocker.Message))
			}
		}
	}

	suspended := sm.GetSuspendedGoals(sessionID)
	if len(suspended) > 0 {
		sb.WriteString("Suspended:\n")
		for _, g := range suspended {
			sb.WriteString(fmt.Sprintf("  %s (%s)\n", g.Intent, goalStatusLabel(g.Status)))
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
			line += fmt.Sprintf(" (%ds", e.ElapsedSeconds)
			if e.MaxSteps > 0 {
				line += fmt.Sprintf(", %d/%d steps", e.StepsUsed, e.MaxSteps)
			}
			line += ")"
		}
		sb.WriteString(line + "\n")
	}
	return strings.TrimSpace(sb.String())
}

func persistentStepMarker(status string) string {
	switch status {
	case "completed":
		return "✓"
	case "doing":
		return "→"
	case "active":
		return "→"
	case "blocked":
		return "!"
	case "failed":
		return "✗"
	default:
		return "☐"
	}
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
		var dur string
		dur = c.Duration.Round(time.Second).String()
		sb.WriteString(fmt.Sprintf("  %s [%s]: %s (PID %d, %s)", marker, c.Handle, c.Status, c.PID, dur))
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
