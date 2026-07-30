package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// chatHostVisible matches renderChatToSurface visibility gates: mounted chat
// rect, no editor/launcher takeover, and an active message list.
func (m *App) chatHostVisible() bool {
	if m.chatHost == nil || m.chatScreen == nil || m.chatRect.Empty() {
		return false
	}
	isEditorActive := m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen()
	isLauncher := m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == ""
	if isEditorActive || isLauncher {
		return false
	}
	return m.activeMessageList() != nil
}

// sidebarHostVisible matches mainSidebarSurf visibility and renderSidebar gates.
func (m *App) sidebarHostVisible() bool {
	if m.Tree == nil {
		return false
	}
	return m.Tree.Visible && m.Tree.Model != nil && m.sidebarHost != nil && m.sidebarScreen != nil
}

// launcherHostVisible matches renderLauncherToSurface visibility gates including
// minimum inner width and chat-area height.
func (m *App) launcherHostVisible() bool {
	if m.launcherScreen == nil || m.launcherHost == nil {
		return false
	}
	if m.AgentMgr.Count() != 0 || m.EditorState.ActiveView != "" {
		return false
	}
	layout := m.computeLayout()
	return layout.InnerW >= 20 && m.ChatAreaHeight >= 3
}

type persistentHostEntry struct {
	priority  int
	active    bool
	host      *ui.Host
	translate func(tea.Msg) tea.Msg
}

func (m *App) persistentHostEntries() []persistentHostEntry {
	return []persistentHostEntry{
		{0, m.chatHostVisible(), m.chatHost, m.translateChatMouse},
		{1, m.sidebarHostVisible(), m.sidebarHost, m.translateSidebarMouse},
		{2, m.launcherHostVisible(), m.launcherHost, m.translateLauncherMouse},
	}
}

// clearChatPointerState cancels chat Host capture and clears selection or
// scrollbar drag state on the chat screen.
func (m *App) clearChatPointerState() {
	if m.chatHost != nil && m.chatHost.HasPointerCapture() {
		m.chatHost.CancelPointerCapture()
	}
	if m.chatScreen != nil {
		m.chatScreen.ClearSelection()
	}
}

// syncPersistentHostCapture cancels pointer capture on product Hosts that are
// hidden or inactive, and resolves duplicate captures to a single winner.
// Invoked from Update before mouse routing so stale drag/selection state cannot
// resurface after mode switches.
func (m *App) syncPersistentHostCapture() {
	entries := m.persistentHostEntries()
	var winner *persistentHostEntry
	for i := range entries {
		e := &entries[i]
		if e.host == nil {
			continue
		}
		if !e.active {
			if e.host.HasPointerCapture() {
				e.host.CancelPointerCapture()
			}
			continue
		}
		if !e.host.HasPointerCapture() {
			continue
		}
		if winner == nil || e.priority < winner.priority {
			if winner != nil {
				winner.host.CancelPointerCapture()
			}
			winner = e
		} else {
			e.host.CancelPointerCapture()
		}
	}
}
