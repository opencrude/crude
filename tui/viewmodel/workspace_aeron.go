package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/workspace"
)

// aeronRole names the session root role of the workspace's Aeron.
const aeronRole = "aeron"

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

// prepareAeronAgent keeps Aeron out of the way: it is minimized, so it never
// takes the user's tab, and it runs with automatic approval. It is safe to
// call again; it acts once the agent is known to this shell.
func (m *App) prepareAeronAgent(id string) {
	if id == "" || m.ws == nil || m.ws.AgentMgr() == nil || m.ws.AgentMgr().Get(id) == nil || m.aeronPrepared[id] {
		return
	}
	if !m.ws.SetYoloForAgent(id, true) {
		return
	}
	if m.aeronPrepared == nil {
		m.aeronPrepared = map[string]bool{}
	}
	m.aeronPrepared[id] = true
	m.MinimizeAgent(id)
}

// taskWorkspaceAeron sends a work order to this workspace's Aeron. It opens
// the Aeron first when the workspace has none. The user keeps their tab; they
// can open Aeron from the Agents panel to see what it does.
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
	return func() tea.Msg {
		return remoteSessionReadyMsg{owner: owner, sequence: sequence, result: owner.NewAgentSessionAs(aeronRole), initial: input, aeron: true}
	}
}
