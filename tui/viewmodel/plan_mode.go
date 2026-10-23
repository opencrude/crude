package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/prompt"
	"crude/provider"
	"crude/shellport"
	"crude/tui/screens/decision"
	"crude/types"
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
	// The session records the change's transcript row for every shell.
	if msg.prompt.Text != "" {
		return m.submitPreparedInput(msg.agentID, msg.prompt, 0)
	}
	return nil
}

// offerPlanImplementation follows a completed Plan mode turn that ends with a
// proposed plan: implement it here, implement it in a new session that starts
// from the plan alone, or keep planning by answering in the composer. It asks
// only in the session the user is looking at.
func (m *App) offerPlanImplementation(agentID string) {
	if !m.ws.PlanModeForAgent(agentID) || m.ActiveSession == nil || m.ws.SessionsByAgent()[agentID] != m.ActiveSession {
		return
	}
	node := m.ws.AgentMgr().Get(agentID)
	if node == nil || node.State == nil || node.State.Messages == nil {
		return
	}
	messages := node.State.Messages.Messages()
	reply := ""
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == types.RoleAssistant {
			reply = messages[index].Content
			break
		}
	}
	plan, ok := proposedPlan(reply)
	if !ok {
		return
	}
	m.openSessionDecision(agentID, "Plan", decision.Spec{
		Purpose: "implement_plan", Title: "Implement this plan?", Hint: "To keep planning, decline and reply in the composer.",
		Facts: []decision.Fact{
			{Label: "Implement", Value: "Plan mode turns off; sends \"" + prompt.PlanImplementationRequest + "\""},
			{Label: "New session", Value: "Implements in a new session that starts from the plan alone"},
		},
		Confirm:        decision.Answer{Label: "Implement", Shortcut: "y"},
		Alternate:      &decision.Answer{Label: "New session", Shortcut: "c"},
		Cancel:         decision.Answer{Label: "Keep planning", Shortcut: "n"},
		SemanticsLabel: "Implement the proposed plan",
	})
	if projection := m.activeSessionProjection(); projection != nil && projection.decisionAgentID == agentID {
		projection.decisionData = plan
	}
}

// proposedPlan returns the Markdown of the last proposed_plan block, whose
// tags stand alone on their own lines.
func proposedPlan(reply string) (string, bool) {
	var plan []string
	var current []string
	open, found := false, false
	for _, line := range strings.Split(reply, "\n") {
		switch strings.TrimSpace(line) {
		case "<proposed_plan>":
			open, current = true, nil
			continue
		case "</proposed_plan>":
			if open {
				plan, found, open = current, true, false
			}
			continue
		}
		if open {
			current = append(current, line)
		}
	}
	text := strings.TrimSpace(strings.Join(plan, "\n"))
	return text, found && text != ""
}

// implementProposedPlan leaves Plan mode, then asks the agent to implement
// the plan it proposed.
func (m *App) implementProposedPlan(agentID string) tea.Cmd {
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	request := provider.PlainInput(prompt.PlanImplementationRequest)
	return func() tea.Msg {
		return planModeResultMsg{owner: owner, generation: generation, agentID: agentID, prompt: request,
			err: owner.SetPlanModeForAgent(agentID, false)}
	}
}

// implementPlanInNewSession starts a new session, outside Plan mode, whose
// first message carries only the plan; the planning session is unchanged.
func (m *App) implementPlanInNewSession(plan string) tea.Cmd {
	if strings.TrimSpace(plan) == "" {
		m.Feedback.NotifyError("No proposed plan to implement")
		return nil
	}
	return m.newSessionWithInput(provider.PlainInput(prompt.PlanImplementationFreshContext(plan)))
}
