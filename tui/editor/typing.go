package editor

import tea "charm.land/bubbletea/v2"

// SetOnTyping observes native text keys, including deletion, line breaks and
// indentation, on the UI owner after source and caret have changed. Deferred
// history input invokes it only when the original key actually executes; paste,
// API edits and canonical publications do not request semantic help. The receiver
// must await canonical acknowledgement.
func (e *Editor) SetOnTyping(fn func(tea.KeyPressMsg)) { e.onTyping = fn }

// Retained recovery/API input must keep the same text/history behavior without
// inventing a new interactive semantic intent in the current shell lifetime.
func (e *Editor) handleNonInteractiveInput(msg tea.Msg) tea.Cmd {
	notify := e.onTyping
	e.onTyping = nil
	defer func() { e.onTyping = notify }()
	return e.HandleEvent(msg)
}
