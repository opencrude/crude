package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/screens/decision"
	"crude/types"
	"crude/workspace"
)

type goalActionResultMsg struct {
	owner      shellport.Workspace
	generation uint64
	agentID    string
	action     string
	err        error
}

const goalUsage = "/goal <objective> starts a goal; /goal pause, resume, edit or clear manage it."

// goalCommand handles /goal. The goal belongs to the selected session's main
// agent, which keeps working on it across turns until an independent
// verifier confirms it.
func (m *App) goalCommand(agentID, argument string) tea.Cmd {
	if agentID == "" {
		m.Feedback.NotifyError("No active session")
		return nil
	}
	if node := m.ws.AgentMgr().OrchestratorFor(agentID); node != nil {
		agentID = node.ID
	}
	argument = strings.TrimSpace(argument)
	verb, rest, _ := strings.Cut(argument, " ")
	goal, exists := m.ws.GoalForAgent(agentID)
	switch strings.ToLower(verb) {
	case "":
		if !exists {
			m.openSessionInfo("Goal", "No goal is set.\n\n"+goalUsage+"\n\nA goal continues across turns and completes only after independent verification.")
			return nil
		}
		m.openSessionInfo("Goal", goalDetails(goal, m.ws.PlanModeForAgent(agentID)))
		return nil
	case workspace.GoalPause, workspace.GoalResume, workspace.GoalClear:
		if rest == "" {
			return m.goalAction(agentID, strings.ToLower(verb), "")
		}
	case workspace.GoalEdit:
		if !exists {
			m.Feedback.NotifyError("No goal to edit. " + goalUsage)
			return nil
		}
		if strings.TrimSpace(rest) == "" {
			// Editing starts from the current objective, in the composer.
			m.Input.SetInput("/goal edit " + goal.Objective)
			return nil
		}
		return m.goalAction(agentID, workspace.GoalEdit, rest)
	}
	if exists && goal.Unfinished() {
		m.openSessionDecision(agentID, "Goal", decision.Spec{
			Purpose: "goal_replace", Title: "Replace the current goal?", Hint: "The current goal stops; the new one starts now.",
			Facts: []decision.Fact{
				{Label: "Current", Value: goal.Objective},
				{Label: "New", Value: argument},
			},
			Confirm:        decision.Answer{Label: "Replace goal", Shortcut: "y"},
			Cancel:         decision.Answer{Label: "Keep current", Shortcut: "n"},
			SemanticsLabel: "Replace the current goal",
		})
		if projection := m.activeSessionProjection(); projection != nil {
			projection.decisionData = argument
		}
		return nil
	}
	return m.goalAction(agentID, workspace.GoalSet, argument)
}

func (m *App) goalAction(agentID, action, objective string) tea.Cmd {
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	return func() tea.Msg {
		_, _, err := owner.GoalAction(agentID, action, objective)
		return goalActionResultMsg{owner: owner, generation: generation, agentID: agentID, action: action, err: err}
	}
}

// applyGoalActionResult reports only failures: the change itself appears as
// a transcript row and in the header in every shell.
func (m *App) applyGoalActionResult(msg goalActionResultMsg) {
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() || msg.err == nil {
		return
	}
	m.Feedback.NotifyError("Goal was not changed: " + msg.err.Error())
}

// offerGoalResume asks, when a session opens with a goal that is not being
// worked on (paused, blocked, or waiting after a reopen or fork), whether to
// continue it.
func (m *App) offerGoalResume(agentID string) {
	if m.queueCoreRestartStartup(agentID) {
		return
	}
	goal, ok := m.ws.GoalForAgent(agentID)
	resumable := goal.Status == types.GoalPaused || goal.Status == types.GoalBlocked || goal.Status == types.GoalActive && goal.Waiting
	if !ok || !resumable {
		return
	}
	facts := []decision.Fact{{Label: "Objective", Value: goal.Objective}, {Label: "Status", Value: goalStatusLabel(goal)}}
	if goal.Reason != "" {
		facts = append(facts, decision.Fact{Label: "Why", Value: goal.Reason})
	}
	m.openSessionDecision(agentID, "Goal", decision.Spec{
		Purpose: "goal_resume", Title: "Resume the goal?", Hint: "Not now keeps it; /goal resume continues it later.",
		Facts:          facts,
		Confirm:        decision.Answer{Label: "Resume goal", Shortcut: "y"},
		Cancel:         decision.Answer{Label: "Not now", Shortcut: "n"},
		SemanticsLabel: "Resume the session goal",
	})
}

func goalStatusLabel(goal types.Goal) string {
	switch {
	case goal.Status == types.GoalActive && goal.Verifying:
		return "checking"
	case goal.Status == types.GoalActive && goal.Waiting:
		return "waiting"
	case goal.Status == types.GoalComplete:
		return "complete"
	case goal.Status == types.GoalBudgetLimited:
		return "limited by budget"
	}
	return string(goal.Status)
}

func goalDetails(goal types.Goal, planMode bool) string {
	status := goalStatusLabel(goal)
	if planMode && goal.Status == types.GoalActive && !goal.Verifying && !goal.Waiting {
		status = "active; automatic continuation waits for Plan mode to end"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Snapshot · reopen /goal to refresh\n\nStatus: %s\n", status)
	if goal.TimeUsed >= time.Second {
		fmt.Fprintf(&text, "Active time: %s\n", formatGoalDuration(goal.TimeUsed))
	}
	if goal.TokenBudget > 0 {
		fmt.Fprintf(&text, "Tokens: %s / %s\n", formatGoalTokens(goal.TokensUsed), formatGoalTokens(goal.TokenBudget))
	} else if goal.TokensUsed > 0 {
		fmt.Fprintf(&text, "Tokens: %s\n", formatGoalTokens(goal.TokensUsed))
	}
	text.WriteString("\nObjective\n" + goal.Objective)
	if goal.Reason != "" {
		text.WriteString("\n\n" + goal.Reason)
	}
	text.WriteString("\n\n" + goalUsage)
	return text.String()
}

func formatGoalDuration(d time.Duration) string {
	minutes := int(d.Minutes())
	switch {
	case minutes < 1:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case minutes < 60:
		return fmt.Sprintf("%dm", minutes)
	case minutes < 24*60:
		return fmt.Sprintf("%dh %dm", minutes/60, minutes%60)
	}
	return fmt.Sprintf("%dd %dh", minutes/(24*60), minutes/60%24)
}

func formatGoalTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprint(n)
}

// goalHeaderText labels the selected session's goal in the header.
func (m *App) goalHeaderText(agentID string) string {
	goal, ok := m.ws.GoalForAgent(agentID)
	if !ok {
		return ""
	}
	label := goalStatusLabel(goal)
	if m.ws.PlanModeForAgent(agentID) && goal.Status == types.GoalActive && !goal.Verifying && !goal.Waiting {
		label = "plan wait"
	}
	return "Goal · " + label
}
