package editor

import tea "charm.land/bubbletea/v2"

// SetOnSignatureHelp observes native '(' and ',' edits on the UI owner after the
// source and caret have changed. Deferred history input invokes it only when the
// original key actually executes; paste, API edits and canonical publications do
// not request semantic help. The receiver must await canonical acknowledgement.
func (e *Editor) SetOnSignatureHelp(fn func()) { e.onSignatureHelp = fn }

// Retained recovery/API input must keep the same text/history behavior without
// inventing a new interactive semantic intent in the current shell lifetime.
func (e *Editor) handleNonInteractiveInput(msg tea.Msg) tea.Cmd {
	notify := e.onSignatureHelp
	e.onSignatureHelp = nil
	defer func() { e.onSignatureHelp = notify }()
	return e.HandleEvent(msg)
}
