package viewmodel

// syncWorkspaceStatusView applies the neutral workspace status snapshot to
// the retained status control. Event handlers still update transient UI-only
// messages and animation state; durable agent/session usage comes from the
// workspace snapshot on every inexpensive shell render.
func (m *App) syncWorkspaceStatusView() {
	if m.StatusBar == nil || m.Workspace == nil {
		return
	}
	view := m.Workspace.StatusView()
	m.StatusBar.SetCurrentAgent(view.CurrentAgentID)
	m.StatusBar.UpdateUsage(
		view.Usage.Prompt,
		view.Usage.Completion,
		view.Usage.Cached,
		view.Usage.Cost,
		view.Usage.ContextWindow,
		view.Usage.ContextUsed,
		view.Usage.Estimated,
	)
}
