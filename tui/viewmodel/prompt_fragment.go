package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/agent/sessiondb"
	"crude/tui/input"
	"crude/workspace"
)

// pasteFragment reproduces composer text this shell copied, with its
// attachments, at the caret of the active composer. Attachments from another
// session are copied into this session off Update; the same session reuses
// them. The paste keeps its place in the ordered paste queue and holds Send.
func (m *App) pasteFragment(event input.PasteFragmentEvent) tea.Cmd {
	owner, store := m.ActiveSession, m.RefStore()
	attachment := input.PasteAttachmentEvent{ID: event.ID, Epoch: event.Epoch}
	if owner == nil || store == nil {
		// No session owns attachments here: the text arrives as typed.
		m.Input.FinishFragmentPaste(event, event.Fragment.Text, nil)
		return nil
	}
	request := pastedTextStoredMsg{
		session: owner, store: store, agentID: m.AgentView.ActiveID(),
		generation: m.workspaceSwitchGen.Load(), event: attachment, fragment: &event,
	}
	m.pastedTextRequests = append(m.pastedTextRequests, request)
	source, _ := event.Fragment.Store.(workspace.SessionReferences)
	return func() tea.Msg {
		request.text, request.refs, request.created, request.err = reproduceFragment(source, store, event.Fragment)
		request.done = true
		return request
	}
}

// reproduceFragment rebuilds fragment text with attachments owned by dst.
// Labels can differ between sessions, so each span takes its new label.
// A failure removes every copy it made and pastes nothing.
func reproduceFragment(source, dst workspace.SessionReferences, fragment input.PromptFragment) (string, []sessiondb.Reference, []string, error) {
	runes := []rune(fragment.Text)
	copies := make(map[string]sessiondb.Reference, len(fragment.Attachments))
	var text strings.Builder
	var refs []sessiondb.Reference
	var created []string
	end := 0
	for _, span := range fragment.Attachments {
		if span.Start < end || span.End > len(runes) || span.Start >= span.End {
			continue
		}
		ref, ok := copies[span.Reference.ID]
		if !ok {
			var err error
			ref, err = reproduceReference(source, dst, span.Reference)
			if err != nil {
				for _, id := range created {
					_ = dst.DeleteReference(id)
				}
				return "", nil, nil, err
			}
			if ref.ID != span.Reference.ID {
				created = append(created, ref.ID)
			}
			copies[span.Reference.ID] = ref
			refs = append(refs, ref)
		}
		text.WriteString(string(runes[end:span.Start]))
		text.WriteString(ref.DisplayName())
		end = span.End
	}
	text.WriteString(string(runes[end:]))
	return text.String(), refs, created, nil
}

// reproduceReference returns dst's attachment for ref. The same session keeps
// the very same attachment; another session receives its own copy.
func reproduceReference(source, dst workspace.SessionReferences, ref sessiondb.Reference) (sessiondb.Reference, error) {
	if source == dst {
		if current, ok := dst.GetReference(ref.ID); ok && current.Active() {
			return current, nil
		}
	}
	return dst.ImportReferenceFrom(source, ref)
}

func (m *App) attachPastedFragment(msg pastedTextStoredMsg) {
	if m.AgentView.ActiveID() != msg.agentID {
		m.Input.CompletePaste(msg.event)
		m.discardFragmentCopies(msg)
		return
	}
	if msg.err != nil {
		m.Input.FinishFragmentPaste(*msg.fragment, "", nil)
		m.Feedback.NotifyError("Could not paste the attachments: " + msg.err.Error() + ". Nothing was pasted; paste again to retry.")
		return
	}
	if !m.Input.FinishFragmentPaste(*msg.fragment, msg.text, msg.refs) {
		m.discardFragmentCopies(msg)
		return
	}
	m.persistComposerDraftIfChanged()
}

// discardFragmentCopies removes attachment copies a stale paste made.
func (m *App) discardFragmentCopies(msg pastedTextStoredMsg) {
	if msg.fragment == nil || len(msg.created) == 0 || msg.store == nil {
		return
	}
	store, created := msg.store, append([]string(nil), msg.created...)
	m.runDetachedAsyncCmd(func() tea.Msg {
		for _, id := range created {
			_ = store.DeleteReference(id)
		}
		return nil
	})
}
