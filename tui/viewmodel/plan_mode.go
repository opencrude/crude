package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/agent/provider"
	"crude/core/port"
)

type planModeResultMsg struct {
	owner      shellport.Workspace
	generation uint64
	agentID    string
	enabled    bool
	prompt     provider.UserInput
	err        error
}

// planModeCommand handles /plan. Alone it switches the session's Plan mode;
// with a prompt it turns Plan mode on and sends the prompt in it. The mode is
// persisted by the session's owner and applies from the next turn.
func (m *App) planModeCommand(agentID string, prompt provider.UserInput) tea.Cmd {
	if agentID == "" {
		m.Feedback.NotifyError("No active session")
		return nil
	}
	prompt.Text = strings.TrimSpace(prompt.Text)
	current := m.ws.PlanModeForAgent(agentID)
	enable := !current || prompt.Text != ""
	if enable == current {
		return m.submitPreparedInput(agentID, prompt, 0)
	}
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	return func() tea.Msg {
		return planModeResultMsg{owner: owner, generation: generation, agentID: agentID, enabled: enable, prompt: prompt,
			err: owner.SetPlanModeForAgent(agentID, enable)}
	}
}

func (m *App) applyPlanModeResult(msg planModeResultMsg) tea.Cmd {
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	if msg.err != nil {
		m.Feedback.NotifyError("Plan mode was not changed: " + msg.err.Error())
		return nil
	}
	if m.ActiveSession != nil && m.ws.SessionsByAgent()[msg.agentID] == m.ActiveSession {
		label := "Plan off · next turn"
		if msg.enabled {
			label = "Plan on · next turn"
		}
		m.Feedback.NotifyInfo(label)
	}
	// The session records the change's transcript row for every shell.
	if msg.prompt.Text != "" {
		return m.submitPreparedInput(msg.agentID, msg.prompt, 0)
	}
	return nil
}
