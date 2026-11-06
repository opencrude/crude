package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/shellchat"
	"crude/tui/ui"
)

// chatFindChord reports whether msg opens find in the active chat.
func (m *App) chatFindChord(msg tea.KeyPressMsg) bool {
	if m.actionRegistry == nil || m.chatScreen == nil || m.rootHost == nil || m.chatRect.Empty() || m.AgentView.ActiveNode() == nil {
		return false
	}
	match := m.actionRegistry.MatchContext("chat", msg)
	return match != nil && match.ID == "chat.find"
}

// openChatFind opens find on the active chat and gives its field the keyboard.
// The bar exists in the host only after a layout, so lay out first.
func (m *App) openChatFind() {
	screen := m.chatScreen
	screen.OpenFind()
	host := m.hostFor(screen.ID())
	host.MarkLayout()
	host.Layout()
	if !host.Focus(screen.FindID()) {
		screen.CloseFind()
		m.Feedback.NotifyInfo("This chat is too short for find. Make the window taller, then press Ctrl+F.")
	}
}

// chatFindOwner returns the chat and host whose find field holds the keyboard.
func (m *App) chatFindOwner() (*shellchat.Screen, *ui.Host) {
	screen := m.chatScreen
	if screen == nil || m.rootHost == nil || !screen.FindOpen() {
		return nil, nil
	}
	if host := m.hostFor(screen.FindID()); host.Focused() == screen.FindID() {
		return screen, host
	}
	return nil, nil
}

// routeChatFind sends a key or paste to the focused find field.
func (m *App) routeChatFind(host *ui.Host, msg tea.Msg) (tea.Model, tea.Cmd) {
	res := host.Handle(msg)
	return m, tea.Batch(res.Cmd, m.handleHostAction(res.Action))
}

// chatFindPending reports whether a visible chat still counts matches, so the
// shell keeps frames coming until the count is final.
func (m *App) chatFindPending() bool {
	return m.chatScreen != nil && m.chatScreen.FindPending()
}

// chatFindHints are the keys of an open find bar.
func chatFindHints() []ui.HintSegment {
	return []ui.HintSegment{ui.ActionSeg("find.hint.navigate"), ui.ActionSeg("find.hint.previous"), ui.ChordSeg("Esc", "close")}
}
