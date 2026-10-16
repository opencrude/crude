package viewmodel

// Shell slot visibility helpers. These gates mirror the slot conditions in
// configureShell: a helper must return true iff the corresponding shellRoot
// slot is active, so viewmodel policy (focus, context menus) and the root
// tree never disagree about what is mounted.

// chatHostVisible matches the shellChatSlot condition: mounted chat rect,
// no editor/launcher takeover, and an active message list.
func (m *App) chatHostVisible() bool {
	if m.chatRect.Empty() || m.chatScreen == nil {
		return false
	}
	if m.isEditorActive() {
		return false
	}
	if m.hostTabActive() {
		return false
	}
	if m.AgentView.VisibleCount() == 0 && m.EditorState.ActiveView == "" {
		return false
	}
	return m.activeMessageList() != nil
}

// sidebarHostVisible matches the shellSidebarSlot condition.
func (m *App) sidebarHostVisible() bool {
	return m.sidebarContentVisible()
}

// sidebarContentVisible is the files/agents/terminals/git pane (not the rail).
func (m *App) sidebarContentVisible() bool {
	if m.Tree == nil || !m.Tree.Visible {
		return false
	}
	if m.activityView == activityFiles {
		return m.Tree.Model != nil && m.sidebarScreen != nil
	}
	return m.activityList != nil
}

// launcherHostVisible matches the shellLauncherSlot condition including
// minimum inner width and chat-area height.
func (m *App) launcherHostVisible() bool {
	if m.Win != nil && (!m.mainAreaEmpty() || m.Win.IsFloating(m.Win.ActiveGroup())) {
		return false
	}
	if m.isGitReviewActive() {
		return false
	}
	if m.launcherScreen == nil {
		return false
	}
	// Agents and editors shown in windows do not hide the launcher; the
	// main area itself is empty.
	if m.Win == nil || len(m.Win.FloatingGroupIDs()) == 0 {
		if m.AgentView.VisibleCount() != 0 || m.EditorState.ActiveView != "" {
			return false
		}
	}
	layout := m.computeLayout()
	return layout.InnerW >= 20 && m.ChatAreaHeight >= 3
}

// clearChatPointerState cancels root-host pointer capture and clears
// selection or scrollbar drag state on the chat screen.
func (m *App) clearChatPointerState() {
	if m.rootHost != nil && m.rootHost.HasPointerCapture() {
		m.rootHost.CancelPointerCapture()
	}
	if m.chatScreen != nil {
		m.chatScreen.ClearSelection()
	}
}
