package viewmodel

import (
	"crude/provider"
	"crude/tui/chatprojection"
	"crude/tui/ui"
	"crude/workspace"
)

func (m *App) AddOrchestrator(orchID string, inputCh chan<- provider.UserInput, btwCh chan<- provider.UserInput, cancelCh chan<- struct{}, blockResolveCh chan string) {
	if m.rootHost != nil {
		m.rootHost.CancelPointerCapture()
	}
	m.launcherScreen = nil
	if m.AgentMgr.Get(orchID) != nil {
		m.switchToAgentView(orchID)
		return
	}

	ml := chatprojection.NewRenderer(m.Theme)
	ml.SetModelName(m.ModelName)
	ml.SetUsername(m.Username)
	ml.SetSubPaneContentFn(m.renderSubPaneContentForApp)

	sp := m.PaneMgr.GetOrCreate(orchID, workspace.PaneAgent, orchID)
	proj := m.subPaneProjections.Get(orchID, workspace.PaneAgent)
	// Replace the projection's auto-created MessageList with the one
	// already configured above (carrying SetSubPaneContentFn). This
	// preserves the existing AddOrchestrator contract while keeping the
	// MessageList ownership on the TUI projection side.
	proj.renderer = ml
	proj.renderer.SetModelName(m.ModelName)
	proj.renderer.SetUsername(m.Username)

	agent := workspace.NewAgentState(orchID, sp.Messages, m.Logger)
	node := m.AgentMgr.AddOrchestrator(orchID, inputCh, btwCh, cancelCh, blockResolveCh, agent)
	// Root orchestrators are the session's default/main agent. Seed category
	// so presentation can show "Main Agent" without leaking orch IDs.
	if node != nil {
		node.Category = "default"
		node.Model = m.ModelName
	}
	m.syncAgentWindowTabs()
}

func (m *App) RemoveOrchestrator(orchID string) {
	m.cleanupAgent(orchID)
}

// cleanupAgent removes all state for a given agent orchestrator or sub-agent.
func (m *App) cleanupAgent(agentID string) {
	m.clearAgentChatScroll(agentID)
	m.subPaneProjections.Remove(agentID)
	m.CloseAgent(agentID)
	m.AgentMgr.Remove(agentID)
	m.PaneMgr.Remove(agentID)
	if m.StatusBar != nil {
		m.StatusBar.ClearAgentState(agentID)
	}
}

func (m *App) activeMsgList() *chatprojection.Renderer {
	n := m.AgentMgr.ActiveNode()
	if n == nil {
		return nil
	}
	if p := m.subPaneProjections.GetExisting(n.ID); p != nil {
		return p.renderer
	}
	return nil
}

func (m *App) completeSubAgent(agentID string) {
	n := m.AgentMgr.Get(agentID)
	if n == nil || n.State == nil {
		return
	}
	if n.State.Completed {
		return
	}

	parentID := ""
	if n.Parent != nil {
		parentID = n.Parent.ID
	}

	title := m.agentTabTitle(agentID)
	if tab := m.Win.TabByTitle(title); tab != nil {
		wasActive := m.Win.IndexOf(tab.ID) == m.Win.ActiveIndex()
		m.Win.CloseTabByID(tab.ID)
		if wasActive && m.AgentMgr.ActiveID() == agentID {
			m.AgentMgr.ClearActive()
			if parentID != "" {
				m.subPaneProjections.InvalidateAllMessages(parentID)
			}
		}
	}

	if sp := m.PaneMgr.Get(agentID); sp != nil && !sp.Done {
		sp.Collapse()
		if parentID != "" {
			m.subPaneProjections.InvalidateSubPane(parentID, agentID)
		}
	}

	n.State.Completed = true
	n.State.Streaming = false
	n.State.Thinking = false
	if n.State.Status == "" || n.State.Status == "running" {
		n.State.Status = "completed"
	}

	m.StatusBar.ClearAgentState(agentID)

	if !m.Win.IsOpen() {
		m.setMode(ModeChat)
	}
}

func (m *App) anyActive() bool {
	if m.AgentMgr.AnyActive() {
		return true
	}
	for _, sp := range m.PaneMgr.Panes() {
		if !sp.Done {
			return true
		}
	}
	return false
}

func (m *App) setMode(newMode Mode) {
	oldMode := m.OverlayState.Current()
	if m.Logger != nil {
		m.Logger.Debug("UI", "mode_switch", "from", modeName(oldMode), "to", modeName(newMode))
	}
	m.OverlayState.Set(newMode)
	if ml := m.activeMsgList(); ml != nil {
		ml.InvalidateAllMessages()
	}
	m.clearChatPointerState()
	switch newMode {
	case ModeTextOverlay:
		m.openTextOverlayWindow()
	case ModeFilePicker:
		m.openFilePickerWindow()
	case ModeChat:
		m.overlayHost.Close(ui.OverlayTextOverlay)
		m.overlayHost.Close(ui.OverlayFilePicker)
	}
}

func (m *App) activeMessageList() *chatprojection.Renderer {
	activeML := m.activeMsgList()
	if activeML != nil {
		return activeML
	}
	// Fallback: use any available agent's projection MessageList
	for _, n := range m.AgentMgr.AllNodes() {
		if p := m.subPaneProjections.GetExisting(n.ID); p != nil {
			return p.renderer
		}
	}
	return nil
}

func (m *App) agentForView() *workspace.AgentState {
	if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil {
		return n.State
	}
	for _, n := range m.AgentMgr.AllNodes() {
		if n.State != nil {
			return n.State
		}
	}
	return nil
}

func (m *App) switchToAgentView(agentID string) {
	prev := m.AgentMgr.ActiveID()
	if agentID == "" {
		m.AgentMgr.ClearActive()
	} else if n := m.AgentMgr.Get(agentID); n != nil && n.State != nil && !n.State.Completed {
		m.AgentMgr.SetActive(agentID)
		m.ActivateSessionByAgent(agentID)
	}

	curr := m.AgentMgr.ActiveID()
	if prev != curr {
		if prev != "" {
			if m.subPaneProjections.Has(prev) {
				m.subPaneProjections.InvalidateAllMessages(prev)
			}
		}
		if curr != "" {
			if m.subPaneProjections.Has(curr) {
				m.subPaneProjections.InvalidateAllMessages(curr)
			}
			if usage, ok := m.AgentUsage(curr); ok && m.StatusBar != nil {
				m.StatusBar.UpdateUsage(usage.Prompt, usage.Completion, usage.Cached, usage.Cost, usage.ContextWindow, usage.ContextUsed, usage.Estimated)
			}
		}
	}

	m.syncAgentWindowTabs()
}

// agentTabTitle returns the window tab title for an agent.
// Uses the full agentID to guarantee uniqueness (no truncation).
// Human-readable display names are set via DisplayTitle on the tab.
func (m *App) agentTabTitle(agentID string) string {
	if agentID == "" {
		return "agent:main"
	}
	return "agent:" + agentID
}

// isAgentTabTitle checks if a window tab title is an agent tab.
func (m *App) isAgentTabTitle(title string) bool {
	return len(title) > 6 && title[:6] == "agent:"
}

// agentIDFromTabTitle extracts the agent ID from a window tab title.
// Since tab titles now use the full agentID, parsing is direct.
func (m *App) agentIDFromTabTitle(title string) string {
	if !m.isAgentTabTitle(title) {
		return ""
	}
	return title[6:]
}

// syncAgentWindowTabs creates/removes window tabs to match the current agent set.
// Every agent gets its own tab.
// Tabs are always shown when at least one agent or editor is open.
func (m *App) syncAgentWindowTabs() {
	// Don't switch away from editor tab
	if m.EditorState.ActiveView != "" {
		m.purgeStaleAgentTabs()
		return
	}

	// If no agents exist, purge agent tabs and return
	if m.AgentMgr.Count() == 0 {
		m.purgeAgentTabs()
		m.EditorState.TabSnapshotCache = ""
		return
	}

	// Determine active agent ID
	activeID := m.AgentMgr.ActiveID()
	tabViews := m.AgentTabViews()
	viewByID := make(map[string]workspace.TabView, len(tabViews))
	for _, view := range tabViews {
		viewByID[view.ID] = view
	}

	// Create tabs for sub-agents that have panes
	for _, id := range m.PaneMgr.PaneOrder() {
		n := m.AgentMgr.Get(id)
		if n == nil || (n.State != nil && n.State.Completed) {
			continue
		}
		view, ok := viewByID[id]
		if !ok {
			continue
		}
		title := m.agentTabTitle(id)
		if m.Win.TabByTitle(title) == nil {
			m.Win.OpenTab(title, "", true)
		}
		if tab := m.Win.TabByTitle(title); tab != nil {
			newIcon := m.agentTabIcon(id)
			if tab.Icon != newIcon {
				m.Win.SetTabIconByIndex(m.Win.IndexOf(tab.ID), newIcon)
			}
			displayLabel := view.DisplayTitle
			if tab.DisplayTitle != displayLabel {
				m.Win.SetTabDisplayTitleByIndex(m.Win.IndexOf(tab.ID), displayLabel)
			}
		}
	}

	// Create tabs for orchestrator agents (top-level agents without parent IDs)
	for _, n := range m.AgentMgr.AllNodes() {
		if n.State != nil && n.State.Completed {
			continue
		}
		if n.Parent != nil {
			continue
		}
		id := n.ID
		view, ok := viewByID[id]
		if !ok {
			continue
		}
		title := m.agentTabTitle(id)
		if m.Win.TabByTitle(title) == nil {
			m.Win.OpenTab(title, "", true)
		}
		if tab := m.Win.TabByTitle(title); tab != nil {
			newIcon := m.agentTabIcon(id)
			if tab.Icon != newIcon {
				m.Win.SetTabIconByIndex(m.Win.IndexOf(tab.ID), newIcon)
			}
			displayLabel := view.DisplayTitle
			if tab.DisplayTitle != displayLabel {
				m.Win.SetTabDisplayTitleByIndex(m.Win.IndexOf(tab.ID), displayLabel)
			}
		}
	}

	// Activate the correct tab
	if activeID != "" {
		activeTitle := m.agentTabTitle(activeID)
		if tab := m.Win.TabByTitle(activeTitle); tab != nil {
			targetIdx := m.Win.IndexOf(tab.ID)
			if m.Win.ActiveIndex() != targetIdx {
				m.Win.SwitchTo(targetIdx)
			}
		}
	}

	m.purgeStaleAgentTabs()
}

// agentTabIcon returns the status icon for an agent's window tab.
func (m *App) agentTabIcon(id string) string {
	if id == "" {
		return "■"
	}
	view, ok := m.agentTabView(id)
	if !ok || view.State == "" {
		return "●"
	}
	status := view.State
	switch {
	case status == "running":
		return "●"
	case status == "completed":
		return "✓"
	case len(status) > 6 && status[:6] == "failed":
		return "✗"
	case status == "cancelled":
		return "!"
	default:
		return "■"
	}
}

func (m *App) agentTabView(id string) (workspace.TabView, bool) {
	for _, view := range m.AgentTabViews() {
		if view.ID == id {
			return view, true
		}
	}
	return workspace.TabView{}, false
}

// purgeAgentTabs closes all agent window tabs in a single pass.
func (m *App) purgeAgentTabs() {
	for i := m.Win.TabCount() - 1; i >= 0; i-- {
		tabs := m.Win.Tabs()
		if i >= len(tabs) {
			continue
		}
		if m.isAgentTabTitle(tabs[i].Title) {
			m.Win.CloseTabByID(tabs[i].ID)
		}
	}
}

// purgeStaleAgentTabs closes agent tabs for agents that no longer exist
// or have completed.
func (m *App) purgeStaleAgentTabs() {
	for i := m.Win.TabCount() - 1; i >= 0; i-- {
		tabs := m.Win.Tabs()
		if i >= len(tabs) {
			continue
		}
		if m.isAgentTabTitle(tabs[i].Title) {
			agentID := m.agentIDFromTabTitle(tabs[i].Title)
			n := m.AgentMgr.Get(agentID)
			if n == nil || (n.State != nil && n.State.Completed) {
				m.Win.CloseTabByID(tabs[i].ID)
			}
		}
	}
}
