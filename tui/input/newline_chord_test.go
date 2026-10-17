package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Every newline chord breaks the composer line, including Ctrl+J, the one
// every terminal delivers (tmux and legacy terminals send Shift+Enter as a
// plain Enter, Windows Terminal takes Alt+Enter). Plain Enter still sends.
func TestComposerNewlineChordsBreakTheLine(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter, Mod: tea.ModShift},
		{Code: tea.KeyEnter, Mod: tea.ModAlt},
		{Code: 'j', Mod: tea.ModCtrl},
	} {
		m, _ := newClipboardModel(t, "ab")
		updated, _, evt := m.Update(key, true, false)
		if evt != nil {
			t.Fatalf("%s sent the draft (%T)", key.String(), evt)
		}
		if got := updated.Value(); got != "ab\n" {
			t.Fatalf("%s produced %q, want a line break", key.String(), got)
		}
	}
	m, _ := newClipboardModel(t, "ab")
	if _, _, evt := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, true, false); evt == nil {
		t.Fatal("plain Enter must send")
	}
}
