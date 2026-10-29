package viewmodel

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/types"
)

const loopUsage = "/loop <interval> <prompt> schedules a recurring task. /loop shows schedules; /loop pause, resume or clear <id> manages one."

type loopActionResultMsg struct {
	owner      shellport.Workspace
	generation uint64
	agentID    string
	action     string
	err        error
}

func parseLoopInterval(value string) (time.Duration, error) {
	value = strings.ToLower(value)
	if strings.HasSuffix(value, "d") {
		days, err := strconv.ParseInt(strings.TrimSuffix(value, "d"), 10, 32)
		if err != nil || days < 1 || days > 7 {
			return 0, fmt.Errorf("use an interval between 1m and 7d")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval < time.Minute || interval > 7*24*time.Hour {
		return 0, fmt.Errorf("use an interval between 1m and 7d")
	}
	return interval, nil
}

func (m *App) loopCommand(agentID, argument string) tea.Cmd {
	if agentID == "" {
		m.Feedback.NotifyError("No active session")
		return nil
	}
	if node := m.ws.AgentMgr().OrchestratorFor(agentID); node != nil {
		agentID = node.ID
	}
	argument = strings.TrimSpace(argument)
	if argument == "" {
		goal, exists := m.ws.GoalForAgent(agentID)
		m.openSessionInfo("Scheduled loops", loopDetails(m.ws.LoopsForAgent(agentID), m.ws.PlanModeForAgent(agentID), exists && goal.Status == types.GoalActive))
		return nil
	}
	verb, rest := argument, ""
	if at := strings.IndexAny(argument, " \t\r\n"); at >= 0 {
		verb, rest = argument[:at], strings.TrimSpace(argument[at:])
	}
	switch strings.ToLower(verb) {
	case "pause", "resume", "clear":
		if rest == "" || strings.ContainsAny(rest, " \t\r\n") {
			m.Feedback.NotifyError("Specify the exact loop ID. " + loopUsage)
			return nil
		}
		return m.loopAction(agentID, strings.ToLower(verb), rest, "", 0)
	}
	interval, err := parseLoopInterval(verb)
	if err != nil || rest == "" {
		m.Feedback.NotifyError("Use /loop 5m <prompt> (interval 1m–7d). " + loopUsage)
		return nil
	}
	return m.loopAction(agentID, "create", "", rest, interval)
}

func (m *App) loopAction(agentID, action, id, task string, interval time.Duration) tea.Cmd {
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	return func() tea.Msg {
		err := owner.LoopAction(agentID, action, id, task, interval)
		return loopActionResultMsg{owner: owner, generation: generation, agentID: agentID, action: action, err: err}
	}
}

func (m *App) applyLoopActionResult(msg loopActionResultMsg) {
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() {
		return
	}
	if msg.err != nil {
		m.Feedback.NotifyError("Loop action: " + msg.err.Error() + " · Check /loop before retrying.")
		return
	}
	if m.ActiveSession != nil && m.ws.SessionsByAgent()[msg.agentID] == m.ActiveSession {
		m.Feedback.NotifyInfo("Loop updated · /loop to inspect")
	}
}

func loopDetails(loops []types.Loop, planMode, goalActive bool) string {
	if len(loops) == 0 {
		return "No scheduled loops.\n\n" + loopUsage + "\n\nLoops run in the retained core, respect session permissions, and expire after seven days."
	}
	var text strings.Builder
	text.WriteString("Snapshot · reopen /loop to refresh\n\n")
	for i, loop := range loops {
		if i != 0 {
			text.WriteString("\n\n")
		}
		status := "active"
		if loop.Paused {
			status = "paused"
		} else if planMode {
			status = "waiting for Plan mode to end"
		} else if goalActive {
			status = "waiting for the active goal"
		}
		fmt.Fprintf(&text, "%s · %s\nEvery %s", loop.ID, status, loop.Interval)
		if !loop.Paused {
			if !loop.NextAt.After(time.Now()) {
				text.WriteString(" · due when available")
			} else {
				fmt.Fprintf(&text, " · next %s", loop.NextAt.Local().Format("2006-01-02 15:04:05"))
			}
		}
		fmt.Fprintf(&text, "\nExpires %s\n\n%s", loop.ExpiresAt.Local().Format("2006-01-02 15:04"), loop.Prompt)
		if loop.Reason != "" {
			text.WriteString("\n\n" + loop.Reason)
		}
	}
	text.WriteString("\n\n" + loopUsage + "\nPausing or clearing does not stop an iteration already running.")
	return text.String()
}
