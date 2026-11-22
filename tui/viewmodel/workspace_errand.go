package viewmodel

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent/provider"
	"crude/internal/retry"
	"crude/types/statuscenter"
	"crude/workspace"
)

const workspaceErrandAction = "workspace-errand"

const (
	// The workspace agent is background inventory. The shell opens it after it
	// has had time to paint and only while the user is not working, so it never
	// competes with a restore, switch or first input. Work for Errand does not
	// wait for a quiet shell: it starts as soon as the lifecycle slot is free.
	errandEnsureStartupDelay = 2 * time.Second
	errandEnsureQuietPeriod  = 3 * time.Second
)

// workspaceErrandAgent returns the root agent of this workspace's open Errand
// session, or "" when none is open.
func (m *App) workspaceErrandAgent() string {
	if m.ws == nil {
		return ""
	}
	for id, sess := range m.ws.SessionsByAgent() {
		if workspace.SessionIsErrand(sess) {
			return id
		}
	}
	return ""
}

func (m *App) isErrandAgent(id string) bool {
	if m.ws == nil || m.ws.AgentMgr() == nil {
		return false
	}
	root := m.ws.AgentMgr().OrchestratorFor(id)
	return root != nil && workspace.SessionIsErrand(m.ws.SessionsByAgent()[root.ID])
}

// agentLabel is the shell's one display name for an agent. The workspace's
// Errand root is always "Errand", not its generated session name, so its tab,
// header, notices, tooltip and prompts name the same agent.
func (m *App) agentLabel(node *workspace.AgentNode) string {
	if node != nil && node.IsOrchestrator() && workspace.SessionIsErrand(m.ws.SessionsByAgent()[node.ID]) {
		return "Errand"
	}
	return workspace.AgentDisplayLabel(node)
}

// agentStatus is the short user text of an agent's state for tab tooltips.
func (m *App) agentStatus(id string) string {
	activity := m.ws.AgentActivity(id)
	switch activity.Status.Phase {
	case statuscenter.AgentApproval, statuscenter.AgentQuestion:
		return "needs an answer"
	case statuscenter.AgentFailed, statuscenter.AgentNeedsAttention, statuscenter.AgentInterrupted:
		return "needs attention"
	case statuscenter.AgentCancelled:
		return "stopped"
	case statuscenter.AgentCompleted:
		return "finished"
	case statuscenter.AgentCancelling:
		return "stopping"
	}
	if activity.Active {
		return "running"
	}
	return "idle"
}

// openWorkspaceErrand opens Errand like any other agent, as a tab. Before the
// core has returned it, the request waits and the tab opens when it arrives.
// A failed start opens Errand's settings, where the cause can be fixed.
func (m *App) openWorkspaceErrand() tea.Cmd {
	// This is an explicit user navigation. The panel must not retain the
	// keyboard after it opens the agent's normal composer.
	m.setFocusInput()
	if id := m.workspaceErrandAgent(); id != "" {
		m.restoreAgent(id)
		return nil
	}
	if m.errandEnsureFailed {
		if m.Feedback != nil && m.errandEnsureError != "" {
			m.Feedback.NotifyError(m.errandEnsureError)
		}
		return m.openApplicationAgent("role/errand")
	}
	m.errandOpenRequested = true
	m.errandEnsureScheduled = true
	m.errandEnsureEarliest = time.Time{}
	return m.ensureWorkspaceErrand()
}

// requestWorkspaceErrandEnsure schedules the background open of Errand. The
// tick starts it after the startup delay and a quiet period without user work.
func (m *App) requestWorkspaceErrandEnsure() {
	if m.ws == nil {
		return
	}
	m.errandEnsureScheduled = true
	m.errandEnsureEarliest = time.Now().Add(errandEnsureStartupDelay)
	m.errandEnsureFailed = false
}

// workspaceErrandTick is the only automatic start point. It yields to user
// work, history restore and any lifecycle operation already in flight.
func (m *App) workspaceErrandTick(now time.Time) tea.Cmd {
	if m.agentPrompt == nil {
		// A waiting Errand question opens when the user pauses.
		for _, state := range m.agentPrompts {
			if !state.inline && !m.errandPromptWaits(state) {
				m.openNextAgentPrompt()
				break
			}
		}
	}
	if !m.errandEnsureScheduled || m.errandEnsureInFlight || m.errandEnsureFailed {
		return nil
	}
	if id := m.workspaceErrandAgent(); id != "" {
		m.errandEnsureScheduled = false
		return m.deliverErrandOrders(id)
	}
	if now.Before(m.errandEnsureEarliest) || m.sessionLifecyclePending || m.workspaceAttachPending ||
		m.historyOperation != nil || m.historySafetyOperation != nil {
		return nil
	}
	if len(m.errandOrders) == 0 && !m.errandOpenRequested && !m.LastInteractionTime.IsZero() && now.Sub(m.LastInteractionTime) < errandEnsureQuietPeriod {
		return nil
	}
	return m.ensureWorkspaceErrand()
}

// ensureWorkspaceErrand asks the core for the workspace's one Errand: it reuses,
// resumes or creates it. The agent reaches this shell as normal inventory and
// never takes the user's tab.
func (m *App) ensureWorkspaceErrand() tea.Cmd {
	if m.ws == nil || m.errandEnsureInFlight || m.workspaceErrandAgent() != "" {
		return nil
	}
	if m.sessionLifecyclePending || m.workspaceAttachPending {
		return nil
	}
	m.errandEnsureScheduled = false
	m.sessionLifecycleSequence++
	sequence, owner := m.sessionLifecycleSequence, m.ws
	m.sessionLifecyclePending = true
	m.errandEnsureInFlight = true
	return m.clientIOCommand(context.Background(), func(context.Context) tea.Msg {
		return remoteSessionReadyMsg{owner: owner, sequence: sequence, result: owner.EnsureErrandSession(), errand: true}
	})
}

// applyErrandEnsure settles one ensure result. Busy admission tries again later.
// Any other failure is reported only when work was waiting for Errand; an
// automatic open stays silent so a new user is not interrupted before setup.
func (m *App) applyErrandEnsure(result *workspace.NewSessionResult) tea.Cmd {
	m.errandEnsureInFlight = false
	if result != nil && result.Error == "" {
		m.errandEnsureFailed = false
		m.errandEnsureError = ""
		if m.ws.AgentMgr().Get(result.OrchID) != nil {
			m.addOrchestrator(result.OrchID, false)
		}
		return m.deliverErrandOrders(result.OrchID)
	}
	if result != nil && result.Busy {
		m.errandEnsureScheduled = true
		m.errandEnsureEarliest = time.Now().Add(retry.Clamp(errandEnsureStartupDelay))
		return nil
	}
	m.errandEnsureFailed = true
	m.errandEnsureScheduled = false
	detail := "Errand could not start. Check the Errand model in Settings."
	switch {
	case result != nil && result.NeedsAccount:
		detail = "Errand needs an account. Choose an account in Settings."
	case result != nil && result.NeedsModel:
		detail = "Errand needs a model. Choose a model in Settings."
	}
	m.errandEnsureError = detail
	if (len(m.errandOrders) > 0 || m.errandOpenRequested) && m.Feedback != nil {
		m.Feedback.NotifyError(detail)
	}
	m.errandOpenRequested = false
	return nil
}

// deliverErrandOrders uses the normal input owner, including its wait for replica
// inventory. Multiple tasks cannot overwrite each other while the agent opens.
func (m *App) deliverErrandOrders(id string) tea.Cmd {
	if len(m.errandOrders) == 0 || id == "" {
		return nil
	}
	commands := make([]tea.Cmd, 0, len(m.errandOrders))
	for _, order := range m.errandOrders {
		commands = append(commands, m.submitFirstInput(id, order))
	}
	m.errandOrders = nil
	return tea.Sequence(commands...)
}

// resetWorkspaceErrand forgets the previous attachment's Errand presentation.
// The session itself remains workspace-owned and is found again on attach.
// Work for the previous workspace's Errand is not carried to another project.
func (m *App) resetWorkspaceErrand() {
	m.errandPrepared = nil
	m.errandEnsureInFlight = false
	m.sessionLifecycleAfterErrand = nil
	m.errandOrders = nil
	m.errandEnsureError = ""
	m.errandOpenRequested = false
	m.requestWorkspaceErrandEnsure()
}

// prepareErrandAgent keeps Errand out of the way: the first time this shell sees
// it, it is minimized unless the user opened it. Approval defaults belong to
// the core session lifecycle; this presentation path does no RPC or persistence.
func (m *App) prepareErrandAgent(id string, open bool) {
	if id == "" || m.ws == nil || m.ws.AgentMgr() == nil || m.ws.AgentMgr().Get(id) == nil || m.errandPrepared[id] {
		return
	}
	if m.errandPrepared == nil {
		m.errandPrepared = map[string]bool{}
	}
	m.errandPrepared[id] = true
	if !open {
		m.minimizeAgent(id)
	}
}

// taskWorkspaceErrand sends a work order to this workspace's Errand. The chat
// shows display, not the full order. When Errand is not open yet, the order
// waits and Errand opens as soon as no other session work runs. The user keeps
// their tab: Errand asks for attention with modal prompts, and the user can
// open it from the Agents panel like any other agent.
func (m *App) taskWorkspaceErrand(text, display string) tea.Cmd {
	input := provider.UserInput{Text: text, DisplayText: display, Origin: provider.OriginHuman}
	if id := m.workspaceErrandAgent(); id != "" {
		m.prepareErrandAgent(id, false)
		return m.submitPreparedInput(id, input, 0)
	}
	m.errandOrders = append(m.errandOrders, input)
	m.errandEnsureScheduled = true
	m.errandEnsureEarliest = time.Time{}
	m.errandEnsureFailed = false
	m.errandEnsureError = ""
	// A busy slot is not an error: the tick starts Errand when it is free.
	return m.ensureWorkspaceErrand()
}
