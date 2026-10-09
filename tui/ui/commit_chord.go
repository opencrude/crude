package ui

import tea "charm.land/bubbletea/v2"

// CommitChord is the shortcut label for "commit from a multi-line field".
const CommitChord = "ctrl+enter"

// IsCommitChord reports whether a key commits a multi-line field or its
// primary action. Ctrl+Enter is canonical; Alt+Enter is accepted everywhere
// Ctrl+Enter is, because terminals and multiplexers without extended keys
// (tmux's default) deliver Ctrl+Enter as a plain Enter, which inserts a
// newline. Ctrl+Alt+Enter stays a distinct chord.
func IsCommitChord(m tea.KeyPressMsg) bool {
	if m.Code != tea.KeyEnter || m.Mod.Contains(tea.ModShift) {
		return false
	}
	ctrl, alt := m.Mod.Contains(tea.ModCtrl), m.Mod.Contains(tea.ModAlt)
	return ctrl != alt
}
