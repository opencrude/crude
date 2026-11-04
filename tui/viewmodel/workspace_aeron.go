package viewmodel

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/workspace"
)

const (
	// The workspace agent is background inventory. Create it after the shell
	// has had time to paint and only while the user is not working, so an
	// automatic session never competes with a restore, switch or first input.
	aeronEnsureStartupDelay = 2 * time.Second
	aeronEnsureQuietPeriod  = 3 * time.Second
	// A temporary admission conflict is retried a bounded number of times. A
	// permanent one is reported once and never retried in a loop.
	aeronEnsureMaxAttempts = 4
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

// requestWorkspaceAeronEnsure schedules the low-priority creation/resume. The
// tick starts it only after the startup delay and a quiet period without user
// work.
func (m *App) requestWorkspaceAeronEnsure() {
	if m.ws == nil {
		return
	}
	m.aeronEnsureScheduled = true
	m.aeronEnsureEarliest = time.Now().Add(aeronEnsureStartupDelay)
	m.aeronEnsureFailed = false
}

// retryWorkspaceAeron schedules one bounded retry of an admission conflict.
// It reports false when the shell has tried often enough and must stop.
func (m *App) retryWorkspaceAeron() bool {
	if m.aeronEnsureAttempts >= aeronEnsureMaxAttempts {
		m.aeronEnsureScheduled = false
		m.aeronEnsureFailed = true
		return false
	}
	m.requestWorkspaceAeronEnsure()
	return true
}

// workspaceAeronTick is the only automatic start point. It yields to any user
// work, history restore or lifecycle operation already in flight.
func (m *App) workspaceAeronTick(now time.Time) tea.Cmd {
	if !m.aeronEnsureScheduled || m.aeronEnsureInFlight || m.aeronEnsureFailed {
		return nil
	}
	if m.workspaceAeronAgent() != "" {
		m.aeronEnsureScheduled = false
		return nil
	}
	if now.Before(m.aeronEnsureEarliest) || m.sessionLifecyclePending || m.workspaceAttachPending ||
		m.historyOperation != nil || m.historySafetyOperation != nil {
		return nil
	}
	if !m.LastInteractionTime.IsZero() && now.Sub(m.LastInteractionTime) < aeronEnsureQuietPeriod {
		return nil
	}
	m.aeronEnsureScheduled = false
	return m.ensureWorkspaceAeron()
}

// aeronEnsureRetryable reports a temporary admission conflict. The scheduler
// retries it after the competing work settles instead of interrupting the user.
func aeronEnsureRetryable(detail string) bool {
	detail = strings.ToLower(detail)
	return strings.Contains(detail, "restore") || strings.Contains(detail, "busy") || strings.Contains(detail, "capacity")
}

// ensureWorkspaceAeron opens or resumes the workspace's permanent Aeron once
// per attachment. The agent is created in the core and reaches this shell as
// normal inventory; it never takes the user's tab.
func (m *App) ensureWorkspaceAeron() tea.Cmd {
	if m.ws == nil || m.aeronEnsureInFlight || m.workspaceAeronAgent() != "" {
		return nil
	}
	if m.sessionLifecyclePending || m.workspaceAttachPending {
		return nil
	}
	m.sessionLifecycleSequence++
	sequence, owner := m.sessionLifecycleSequence, m.ws
	m.sessionLifecyclePending = true
	m.aeronEnsureInFlight = true
	m.aeronEnsureAttempts++
	return func() tea.Msg {
		return remoteSessionReadyMsg{owner: owner, sequence: sequence, result: owner.EnsureAeronSession(), aeron: true}
	}
}

// resetWorkspaceAeron forgets the previous attachment's Aeron presentation.
// The session itself remains workspace-owned and is found again on attach.
func (m *App) resetWorkspaceAeron() {
	m.aeronPrepared = nil
	m.aeronEnsureInFlight = false
	m.aeronEnsureFailed = false
	m.aeronEnsureAttempts = 0
	m.requestWorkspaceAeronEnsure()
}

// prepareAeronAgent keeps Aeron out of the way: it is minimized, so it never
// takes the user's tab. Automatic approval is best effort; a failed policy
// update must not make the agent disappear. It is safe to call again; it acts
// once the agent is known to this shell.
func (m *App) prepareAeronAgent(id string) {
	if id == "" || m.ws == nil || m.ws.AgentMgr() == nil || m.ws.AgentMgr().Get(id) == nil || m.aeronPrepared[id] {
		return
	}
	if m.aeronPrepared == nil {
		m.aeronPrepared = map[string]bool{}
	}
	m.aeronPrepared[id] = true
	m.ws.SetYoloForAgent(id, true)
	m.minimizeAgent(id)
}

// taskWorkspaceAeron sends a work order to this workspace's Aeron. It opens or
// resumes the Aeron first when the shell does not know it yet. The user keeps
// their tab; they can open Aeron from the Agents panel to see what it does.
func (m *App) taskWorkspaceAeron(text string) tea.Cmd {
	input := provider.PlainInput(text)
	if id := m.workspaceAeronAgent(); id != "" {
		m.prepareAeronAgent(id)
		return m.submitPreparedInput(id, input, 0)
	}
	if m.sessionLifecyclePending || m.workspaceAttachPending {
		if m.Feedback != nil {
			m.Feedback.NotifyInfo("A session is still opening; set up the tools again when it is ready")
		}
		return nil
	}
	m.sessionLifecycleSequence++
	sequence, owner := m.sessionLifecycleSequence, m.ws
	m.sessionLifecyclePending = true
	m.aeronEnsureInFlight = true
	return func() tea.Msg {
		return remoteSessionReadyMsg{owner: owner, sequence: sequence, result: owner.EnsureAeronSession(), initial: input, aeron: true}
	}
}
