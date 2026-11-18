package input

import (
	tea "charm.land/bubbletea/v2"

	"crude/agent/sessiondb"
	"crude/history"
	"crude/tui/rendering"
	"crude/tui/ui"
)

// PromptFragment is composer text copied or cut together with the
// attachments it shows. The system clipboard carries only Text (attachment
// labels for other applications); pasting exactly that text into a composer
// reproduces the attachments too.
type PromptFragment struct {
	Text string
	// Attachments bind reference snapshots to exact rune spans of Text.
	Attachments []history.Attachment
	// Store is the reference owner the attachments were copied from.
	Store ReferenceStore
}

// PromptClipboard is the shell's memory of the last composer copy. Every
// composer of one shell shares it, so a prompt copied from one agent tab or
// session pastes with its attachments into another.
type PromptClipboard struct{ last *PromptFragment }

// NewPromptClipboard creates the shared memory for one shell.
func NewPromptClipboard() *PromptClipboard { return &PromptClipboard{} }

func (c *PromptClipboard) remember(fragment PromptFragment) {
	if c == nil {
		return
	}
	if len(fragment.Attachments) == 0 {
		// Plain text pastes exactly as text; forget an older fragment so its
		// attachments cannot reappear for unrelated text.
		c.last = nil
		return
	}
	c.last = &fragment
}

// match reports the remembered fragment whose text a paste carries.
func (c *PromptClipboard) match(content string) (PromptFragment, bool) {
	if c == nil || c.last == nil || normalizedPaste(c.last.Text) != content {
		return PromptFragment{}, false
	}
	return *c.last, true
}

func normalizedPaste(text string) string {
	return rendering.SanitizeForTerminal(ui.PastedLineBreaks(text))
}

// PasteFragmentEvent asks the shell to reproduce a pasted fragment's
// attachments in the destination session. Like a large paste it holds Send
// until it completes, and it lands at the caret it was pasted at.
type PasteFragmentEvent struct {
	ID, Epoch uint64
	Fragment  PromptFragment
}

func (PasteFragmentEvent) event() {}

// SetClipboardCopy routes this composer's clipboard writes (and those of
// drafts made from it) through the host's clipboard owner.
func (m *Model) SetClipboardCopy(fn func(string) tea.Cmd) {
	m.copyText = fn
	m.input.SetClipboardCopy(fn)
}

// SetPromptClipboard shares one shell's copy memory with this composer.
func (m *Model) SetPromptClipboard(clipboard *PromptClipboard) { m.clipboard = clipboard }

// PromptClipboard returns the copy memory this composer records into.
func (m Model) PromptClipboard() *PromptClipboard { return m.clipboard }

// Fragment returns draft runes [start,end) with the attachments wholly inside.
func (m Model) Fragment(start, end int) PromptFragment {
	runes := []rune(m.input.Value())
	start, end = max(0, min(start, len(runes))), max(0, min(end, len(runes)))
	if start > end {
		start, end = end, start
	}
	fragment := PromptFragment{Text: string(runes[start:end]), Store: m.refStore}
	if m.refStore == nil {
		return fragment
	}
	for _, rr := range m.RefRanges() {
		if rr.Start < start || rr.End > end {
			continue
		}
		ref, ok := m.refStore.GetReference(rr.ID)
		if !ok || !ref.Active() {
			continue
		}
		fragment.Attachments = append(fragment.Attachments, history.Attachment{Start: rr.Start - start, End: rr.End - start, Reference: ref})
	}
	return fragment
}

// RememberFragment records draft runes [start,end) as the latest copy.
func (m Model) RememberFragment(start, end int) {
	m.clipboard.remember(m.Fragment(start, end))
}

// rememberClipboardChord records what a copy or cut chord is about to put on
// the clipboard: the selection, or the whole draft for a cut without one.
func (m Model) rememberClipboardChord(chord ui.ClipboardChord) {
	start, end := m.input.SelectionRange()
	switch {
	case start != end && (chord == ui.ClipboardChordCopy || chord == ui.ClipboardChordCut):
		m.RememberFragment(start, end)
	case chord == ui.ClipboardChordCut && m.input.Value() != "":
		m.RememberFragment(0, len([]rune(m.input.Value())))
	}
}

// beginFragmentPaste reserves an ordered pending paste for a fragment.
func (m *Model) beginFragmentPaste(fragment PromptFragment) PasteFragmentEvent {
	m.pasteSequence++
	if m.pendingPastes == nil {
		m.pendingPastes = make(map[uint64]bool)
	}
	m.pendingPastes[m.pasteSequence] = true
	return PasteFragmentEvent{ID: m.pasteSequence, Epoch: m.draftEpoch, Fragment: fragment}
}

// FragmentPasteCurrent reports whether the exact paste gesture still waits.
func (m Model) FragmentPasteCurrent(event PasteFragmentEvent) bool {
	return event.Epoch == m.draftEpoch && m.pendingPastes[event.ID]
}

// FinishFragmentPaste settles one fragment paste. With refs it inserts the
// reproduced text at the caret and binds its attachments; without, it only
// releases the Send barrier.
func (m *Model) FinishFragmentPaste(event PasteFragmentEvent, text string, refs []sessiondb.Reference) bool {
	if !m.FragmentPasteCurrent(event) {
		return false
	}
	delete(m.pendingPastes, event.ID)
	if text == "" {
		return true
	}
	if m.promptReferenceIDs != nil {
		for _, ref := range refs {
			m.promptReferenceIDs[ref.ID] = true
		}
	}
	if m.input.HasActiveSelection() {
		m.input.DeleteTextSelection()
	}
	m.input.InsertString(text)
	m.applyReferenceRanges()
	m.syncSlashState()
	m.draftRevision++
	return true
}
