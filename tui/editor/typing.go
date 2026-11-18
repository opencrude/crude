package editor

import tea "charm.land/bubbletea/v2"

// SetOnTyping observes native text keys, including deletion, line breaks and
// indentation, on the UI owner after source and caret have changed. Deferred
// history input invokes it only when the original key actually executes; paste,
// API edits and canonical publications do not request semantic help. The receiver
// must await canonical acknowledgement.
func (e *Editor) SetOnTyping(fn func(tea.KeyPressMsg)) { e.onTyping = fn }

// GhostOutcome names what the user did with shown inline text.
type GhostOutcome int

const (
	// GhostAccepted: Tab took all of the text (or the whole replacement).
	GhostAccepted GhostOutcome = iota + 1
	// GhostAcceptedPart: Ctrl+Right or Alt+End took a word or a line; the
	// rest stays shown.
	GhostAcceptedPart
	// GhostTypedThrough: the user typed the whole text.
	GhostTypedThrough
	// GhostJumped: the first Tab moved the caret to a marker's place.
	GhostJumped
)

// SetOnGhostOutcome observes, on the UI owner, how the user used a shown
// suggestion after the edit or caret move. Dismissal is the caller's own Esc.
func (e *Editor) SetOnGhostOutcome(fn func(owner string, outcome GhostOutcome)) {
	e.onGhostOutcome = fn
}

func (e *Editor) ghostOutcome(owner string, outcome GhostOutcome) {
	if e.onGhostOutcome != nil && owner != "" {
		e.onGhostOutcome(owner, outcome)
	}
}

// Retained recovery/API input must keep the same text/history behavior without
// inventing a new interactive semantic intent in the current shell lifetime.
func (e *Editor) handleNonInteractiveInput(msg tea.Msg) tea.Cmd {
	notify, outcome := e.onTyping, e.onGhostOutcome
	e.onTyping, e.onGhostOutcome = nil, nil
	defer func() { e.onTyping, e.onGhostOutcome = notify, outcome }()
	return e.HandleEvent(msg)
}
