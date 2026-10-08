package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"strings"

	"crude/sessiondb"
	"crude/tui/input"
	"crude/workspace"
)

type pastedTextStoredMsg struct {
	session    *workspace.Session
	store      *workspace.ReferenceStore
	agentID    string
	generation uint64
	event      input.PasteAttachmentEvent
	ref        sessiondb.Reference
	err        error
	done       bool
}

func (m *App) storePastedText(event input.PasteAttachmentEvent) tea.Cmd {
	owner, store := m.ActiveSession, m.RefStore()
	if owner == nil || store == nil {
		m.Input.CompletePaste(event)
		m.Feedback.NotifyError("Open an agent session before attaching pasted text")
		return nil
	}
	kind, inline := sessiondb.KindPastedText, input.PastedTextInline(event.Content)
	if !inline {
		kind = sessiondb.KindPastedTextFile
	}
	result := pastedTextStoredMsg{
		session: owner, store: store, agentID: m.AgentMgr.ActiveID(),
		generation: m.workspaceSwitchGen.Load(), event: event,
	}
	m.pastedTextRequests = append(m.pastedTextRequests, result)
	m.Feedback.NotifyInfo("Attaching pasted text…")
	return func() tea.Msg {
		result.ref, result.err = store.CreateReference(sessiondb.CreateRequest{
			Kind: kind, Title: fmt.Sprintf("Pasted text · %d lines", strings.Count(event.Content, "\n")+1),
			Mime: "text/plain; charset=utf-8", Content: event.Content, Inline: inline,
		})
		result.done = true
		return result
	}
}

func (m *App) applyPastedText(msg pastedTextStoredMsg) {
	for index, request := range m.pastedTextRequests {
		if request.session == msg.session && request.event.ID == msg.event.ID && request.event.Epoch == msg.event.Epoch {
			m.pastedTextRequests[index] = msg
			break
		}
	}
	// Storage can complete out of order. Keep accepted paste gestures in input
	// order while allowing typing; discard invalidated draft owners immediately.
	for len(m.pastedTextRequests) > 0 {
		request := m.pastedTextRequests[0]
		sameOwner := request.generation == m.workspaceSwitchGen.Load() && request.session == m.ActiveSession && request.store == m.RefStore()
		current := sameOwner && request.agentID == m.AgentMgr.ActiveID() && m.Input.PasteCurrent(request.event)
		if current && !request.done {
			break
		}
		m.pastedTextRequests[0] = pastedTextStoredMsg{}
		m.pastedTextRequests = m.pastedTextRequests[1:]
		if len(m.pastedTextRequests) == 0 {
			m.pastedTextRequests = nil
		}
		if !current {
			// Root/child tabs may share the same draft, but another session's
			// first paste can reuse the numeric event ID/epoch. Never settle it.
			if sameOwner {
				m.Input.CompletePaste(request.event)
			}
			continue
		}
		m.attachPastedText(request)
	}
}

func (m *App) attachPastedText(msg pastedTextStoredMsg) {
	if msg.generation != m.workspaceSwitchGen.Load() || m.ActiveSession != msg.session || m.RefStore() != msg.store {
		return
	}
	if !m.Input.CompletePaste(msg.event) {
		return
	}
	if m.AgentMgr.ActiveID() != msg.agentID {
		return
	}
	if msg.err != nil {
		m.Feedback.NotifyError("Could not attach pasted text: " + msg.err.Error() + ". The draft was kept; paste again to retry.")
		return
	}
	m.Input.InsertReference(msg.ref)
	m.persistComposerDraftIfChanged()
	m.Feedback.NotifyInfo("Attached pasted text · click the attachment to view or edit")
}
