package viewmodel

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/retry"
	"crude/types"
	"crude/workspace"
)

const workspaceAeronAction = "workspace-aeron"

const (
	// The workspace agent is background inventory. The shell opens it after it
	// has had time to paint and only while the user is not working, so it never
	// competes with a restore, switch or first input. Work for Aeron does not
	// wait for a quiet shell: it starts as soon as the lifecycle slot is free.
	aeronEnsureStartupDelay = 2 * time.Second
	aeronEnsureQuietPeriod  = 3 * time.Second
)

// workspaceAeronAgent returns the root agent of this workspace's open Aeron
// session, or "" when none is open.
func (m *App) workspaceAeronAgent() string {
	if m.ws == nil {
		return ""
	}
	for id, sess := range m.ws.SessionsByAgent() {
		if workspace.SessionIsAeron(sess) {
			return id
		}
	}
	return ""
}

func (m *App) isAeronAgent(id string) bool {
	if m.ws == nil || m.ws.AgentMgr() == nil {
		return false
	}
	root := m.ws.AgentMgr().OrchestratorFor(id)
	return root != nil && workspace.SessionIsAeron(m.ws.SessionsByAgent()[root.ID])
}

// agentLabel is the shell's one display name for an agent. The workspace's
// Aeron root is always "Aeron", not its generated session name, so its tab,
// header, notices, tooltip and prompts name the same agent.
func (m *App) agentLabel(node *workspace.AgentNode) string {
	if node != nil && node.IsOrchestrator() && workspace.SessionIsAeron(m.ws.SessionsByAgent()[node.ID]) {
		return "Aeron"
	}
	return workspace.AgentDisplayLabel(node)
}

// agentStatus is the short user text of an agent's state for tab tooltips.
func (m *App) agentStatus(id string) string {
	activity := m.ws.AgentActivity(id)
	switch activity.Status.Phase {
	case types.AgentApproval, types.AgentQuestion:
		return "needs an answer"
	case types.AgentFailed, types.AgentNeedsAttention, types.AgentInterrupted:
		return "needs attention"
	case types.AgentCancelled:
		return "stopped"
	case types.AgentCompleted:
		return "finished"
	case types.AgentCancelling:
		return "stopping"
	}
	if activity.Active {
		return "running"
	}
	return "idle"
}

// openWorkspaceAeron opens Aeron like any other agent, as a tab. Before the
// core has returned it, the request waits and the tab opens when it arrives.
// A failed start opens Aeron's settings, where the cause can be fixed.
func (m *App) openWorkspaceAeron() tea.Cmd {
	// This is an explicit user navigation. The panel must not retain the
	// keyboard after it opens the agent's normal composer.
	m.setFocusInput()
	if id := m.workspaceAeronAgent(); id != "" {
		m.restoreAgent(id)
		return nil
	}
	if m.aeronEnsureFailed {
		if m.Feedback != nil && m.aeronEnsureError != "" {
			m.Feedback.NotifyError(m.aeronEnsureError)
		}
		return m.openApplicationAgent("role/aeron")
	}
	m.aeronOpenRequested = true
	m.aeronEnsureScheduled = true
	m.aeronEnsureEarliest = time.Time{}
	return m.ensureWorkspaceAeron()
}

// requestWorkspaceAeronEnsure schedules the background open of Aeron. The
// tick starts it after the startup delay and a quiet period without user work.
func (m *App) requestWorkspaceAeronEnsure() {
	if m.ws == nil {
		return
	}
	m.aeronEnsureScheduled = true
	m.aeronEnsureEarliest = time.Now().Add(aeronEnsureStartupDelay)
	m.aeronEnsureFailed = false
}

// workspaceAeronTick is the only automatic start point. It yields to user
// work, history restore and any lifecycle operation already in flight.
func (m *App) workspaceAeronTick(now time.Time) tea.Cmd {
	if !m.aeronEnsureScheduled || m.aeronEnsureInFlight || m.aeronEnsureFailed {
		return nil
	}
	if id := m.workspaceAeronAgent(); id != "" {
		m.aeronEnsureScheduled = false
		return m.deliverAeronOrders(id)
	}
	if now.Before(m.aeronEnsureEarliest) || m.sessionLifecyclePending || m.workspaceAttachPending ||
		m.historyOperation != nil || m.historySafetyOperation != nil {
		return nil
	}
	if len(m.aeronOrders) == 0 && !m.aeronOpenRequested && !m.LastInteractionTime.IsZero() && now.Sub(m.LastInteractionTime) < aeronEnsureQuietPeriod {
		return nil
	}
	return m.ensureWorkspaceAeron()
}

// ensureWorkspaceAeron asks the core for the workspace's one Aeron: it reuses,
// resumes or creates it. The agent reaches this shell as normal inventory and
// never takes the user's tab.
func (m *App) ensureWorkspaceAeron() tea.Cmd {
	if m.ws == nil || m.aeronEnsureInFlight || m.workspaceAeronAgent() != "" {
		return nil
	}
	if m.sessionLifecyclePending || m.workspaceAttachPending {
		return nil
	}
	m.aeronEnsureScheduled = false
	m.sessionLifecycleSequence++
	sequence, owner := m.sessionLifecycleSequence, m.ws
	m.sessionLifecyclePending = true
	m.aeronEnsureInFlight = true
	return m.clientIOCommand(context.Background(), func(context.Context) tea.Msg {
		return remoteSessionReadyMsg{owner: owner, sequence: sequence, result: owner.EnsureAeronSession(), aeron: true}
	})
}

// applyAeronEnsure settles one ensure result. Busy admission tries again later.
// Any other failure is reported only when work was waiting for Aeron; an
// automatic open stays silent so a new user is not interrupted before setup.
func (m *App) applyAeronEnsure(result *workspace.NewSessionResult) tea.Cmd {
	m.aeronEnsureInFlight = false
	if result != nil && result.Error == "" {
		m.aeronEnsureFailed = false
		m.aeronEnsureError = ""
		if m.ws.AgentMgr().Get(result.OrchID) != nil {
			m.addOrchestrator(result.OrchID, false)
		}
		return m.deliverAeronOrders(result.OrchID)
	}
	if result != nil && result.Busy {
		m.aeronEnsureScheduled = true
		m.aeronEnsureEarliest = time.Now().Add(retry.Clamp(aeronEnsureStartupDelay))
		return nil
	}
	m.aeronEnsureFailed = true
	m.aeronEnsureScheduled = false
	detail := "Aeron could not start. Check the Aeron model in Settings."
	switch {
	case result != nil && result.NeedsAccount:
		detail = "Aeron needs an account. Choose an account in Settings."
	case result != nil && result.NeedsModel:
		detail = "Aeron needs a model. Choose a model in Settings."
	}
	m.aeronEnsureError = detail
	if (len(m.aeronOrders) > 0 || m.aeronOpenRequested) && m.Feedback != nil {
		m.Feedback.NotifyError(detail)
	}
	m.aeronOpenRequested = false
	return nil
}

// deliverAeronOrders uses the normal input owner, including its wait for replica
// inventory. Multiple tasks cannot overwrite each other while the agent opens.
func (m *App) deliverAeronOrders(id string) tea.Cmd {
	if len(m.aeronOrders) == 0 || id == "" {
		return nil
	}
	commands := make([]tea.Cmd, 0, len(m.aeronOrders))
	for _, order := range m.aeronOrders {
		commands = append(commands, m.submitFirstInput(id, order))
	}
	m.aeronOrders = nil
	return tea.Sequence(commands...)
}

// resetWorkspaceAeron forgets the previous attachment's Aeron presentation.
// The session itself remains workspace-owned and is found again on attach.
// Work for the previous workspace's Aeron is not carried to another project.
func (m *App) resetWorkspaceAeron() {
	m.aeronPrepared = nil
	m.aeronEnsureInFlight = false
	m.aeronOrders = nil
	m.aeronEnsureError = ""
	m.aeronOpenRequested = false
	m.requestWorkspaceAeronEnsure()
}

// prepareAeronAgent keeps Aeron out of the way: the first time this shell sees
// it, it is minimized unless the user opened it. Approval defaults belong to
// the core session lifecycle; this presentation path does no RPC or persistence.
func (m *App) prepareAeronAgent(id string, open bool) {
	if id == "" || m.ws == nil || m.ws.AgentMgr() == nil || m.ws.AgentMgr().Get(id) == nil || m.aeronPrepared[id] {
		return
	}
	if m.aeronPrepared == nil {
		m.aeronPrepared = map[string]bool{}
	}
	m.aeronPrepared[id] = true
	if !open {
		m.minimizeAgent(id)
	}
}

// taskWorkspaceAeron sends a work order to this workspace's Aeron. The chat
// shows display, not the full order. When Aeron is not open yet, the order
// waits and Aeron opens as soon as no other session work runs. The user keeps
// their tab: Aeron asks for attention with modal prompts, and the user can
// open it from the Agents panel like any other agent.
func (m *App) taskWorkspaceAeron(text, display string) tea.Cmd {
	input := provider.UserInput{Text: text, DisplayText: display, Origin: provider.OriginHuman}
	if id := m.workspaceAeronAgent(); id != "" {
		m.prepareAeronAgent(id, false)
		return m.submitPreparedInput(id, input, 0)
	}
	m.aeronOrders = append(m.aeronOrders, input)
	m.aeronEnsureScheduled = true
	m.aeronEnsureEarliest = time.Time{}
	m.aeronEnsureFailed = false
	m.aeronEnsureError = ""
	// A busy slot is not an error: the tick starts Aeron when it is free.
	return m.ensureWorkspaceAeron()
}
