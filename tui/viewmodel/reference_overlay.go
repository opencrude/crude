package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/screens/textoverlay"
	"crude/tui/ui"
	"crude/workspace"
)

type referenceOverlayState struct {
	session    *workspace.Session
	store      workspace.SessionReferences
	screen     *textoverlay.Screen
	ref        sessiondb.Reference
	original   string
	generation uint64
	loaded     bool
	saving     bool
}

type referenceContentLoadedMsg struct {
	owner    *referenceOverlayState
	content  string
	readOnly bool
	err      error
}

type referenceContentSavedMsg struct {
	owner *referenceOverlayState
	err   error
}

func (m *App) openReferenceOverlay(id string) {
	m.runDetachedAsyncCmd(m.beginReferenceOverlay(id))
}

func (m *App) beginReferenceOverlay(id string) tea.Cmd {
	store, screen := m.RefStore(), m.sessionTextOverlay()
	if store == nil || screen == nil {
		return nil
	}
	ref, ok := store.GetReference(id)
	if !ok || !ref.Active() {
		return nil
	}
	// Close the old owner before opening new content; closing the old frame
	// after Open would clear this same retained screen's new value.
	m.overlayHost.Close(ui.OverlayTextOverlay)
	screen.OpenReadOnly(id, ref.DisplayName(), "Loading attachment…")
	m.setMode(ModeTextOverlay)
	state := &referenceOverlayState{session: m.ActiveSession, store: store, screen: screen, ref: ref, generation: m.workspaceSwitchGen.Load()}
	m.referenceOverlay = state
	return func() tea.Msg {
		content, readOnly, err := store.ReadReferenceContent(id)
		return referenceContentLoadedMsg{owner: state, content: content, readOnly: readOnly, err: err}
	}
}

func (m *App) ownsReferenceOverlay(state *referenceOverlayState) bool {
	return state != nil && m.referenceOverlay == state && state.session == m.ActiveSession &&
		state.store == m.RefStore() && state.generation == m.workspaceSwitchGen.Load() &&
		state.screen == m.sessionTextOverlay() && m.overlayHost.IsOpen(ui.OverlayTextOverlay)
}

func (m *App) markReferenceOverlay() {
	if window := m.overlayHost.Window(ui.OverlayTextOverlay); window != nil {
		if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
			frame.Host().RegisterTree(frame.Host().Root())
			frame.Host().MarkLayout()
		}
		window.MarkContentDirty()
	}
}

func (m *App) applyReferenceContent(msg referenceContentLoadedMsg) {
	state := msg.owner
	if !m.ownsReferenceOverlay(state) {
		return
	}
	if msg.err != nil {
		state.screen.OpenReadOnly(state.ref.ID, state.ref.DisplayName(), "Attachment could not be opened.\n"+msg.err.Error())
		m.Feedback.NotifyError("Could not open attachment: " + msg.err.Error())
	} else {
		current, ok := state.store.GetReference(state.ref.ID)
		if !ok || current != state.ref {
			m.setMode(ModeChat)
			m.Feedback.NotifyError("The attachment changed while it was loading; open it again")
			return
		}
		state.original, state.loaded = msg.content, true
		if msg.readOnly {
			state.screen.OpenReadOnly(state.ref.ID, state.ref.DisplayName(), msg.content)
		} else {
			state.screen.Open(state.ref.ID, state.ref.DisplayName(), msg.content)
		}
	}
	m.markReferenceOverlay()
	state.screen.FocusInput()
}

func (m *App) saveReferenceFromOverlay(msg textoverlay.SaveMsg) tea.Cmd {
	state := m.referenceOverlay
	if !m.ownsReferenceOverlay(state) || !state.loaded || state.saving || state.screen.ReadOnly() {
		return nil
	}
	state.saving = true
	state.screen.SetBusy(true)
	m.markReferenceOverlay()
	return func() tea.Msg {
		err := state.store.SaveReferenceContent(state.ref, state.original, msg.Text)
		return referenceContentSavedMsg{owner: state, err: err}
	}
}

func (m *App) applyReferenceSave(msg referenceContentSavedMsg) {
	if !m.ownsReferenceOverlay(msg.owner) {
		return
	}
	if msg.err != nil {
		msg.owner.saving = false
		msg.owner.screen.SetBusy(false)
		m.markReferenceOverlay()
		m.Feedback.NotifyError("Could not save attachment: " + msg.err.Error() + ". Your edits are kept.")
		return
	}
	m.setMode(ModeChat)
	m.persistComposerDraftIfChanged()
	m.Feedback.NotifyInfo("Pasted text saved")
}
