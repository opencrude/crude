package viewmodel

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

func (m *App) AddOrchestrator(orchID string) {
	if m.rootHost != nil {
		m.rootHost.CancelPointerCapture()
	}
	m.launcherScreen = nil
	if m.AgentMgr.Get(orchID) != nil {
		m.switchToAgentView(orchID)
		return
	}

	ml := chatprojection.NewRenderer(m.Theme)
	ml.SetLegacyModelFallback(m.ModelName)
	ml.SetUsername(m.Username)
	ml.SetSubPaneContentFn(m.renderSubPaneContentForApp)

	sp := m.PaneMgr.GetOrCreate(orchID, workspace.PaneAgent, orchID)
	proj := m.subPaneProjections.Get(orchID, workspace.PaneAgent)
	// Replace the projection's auto-created MessageList with the one
	// already configured above (carrying SetSubPaneContentFn). This
	// preserves the existing AddOrchestrator contract while keeping the
	// MessageList ownership on the TUI projection side.
	proj.renderer = ml
	proj.renderer.SetLegacyModelFallback(m.ModelName)
	proj.renderer.SetUsername(m.Username)

	agent := workspace.NewAgentState(orchID, sp.Messages, m.Logger)
	node := m.AgentMgr.AddOrchestrator(orchID, agent)
	// Bind the durable session identity; task/model labels never own names.
	if node != nil {
		node.Category = "default"
		node.Model = m.ModelName
		node.Label = m.AgentNameFor(orchID)
		sp.Title = node.Label
	}
	for _, childID := range m.HydrateCompletedSubAgents(orchID) {
		child := m.AgentMgr.Get(childID)
		pane := m.PaneMgr.Get(childID)
		if child == nil || pane == nil {
			continue
		}
		projection := m.subPaneProjections.Get(childID, workspace.PaneAgent)
		projection.renderer.SetLegacyModelFallback(child.Model)
		projection.renderer.SetUsername(m.Username)
		projection.renderer.SetSubPaneContentFn(m.renderSubPaneContentForApp)
		projection.renderer.ScrollToTop()
		if m.agentChatScroll == nil {
			m.agentChatScroll = make(map[string]agentMainChatScrollSnapshot)
		}
		m.agentChatScroll[childID] = agentMainChatScrollSnapshot{Offset: 0, AutoScroll: false}
	}
	m.syncAgentStatusInventory()
	m.syncAgentWindowTabs()
	m.activateAgentWindowTab(orchID)
}

func (m *App) RemoveOrchestrator(orchID string) {
	m.cleanupAgent(orchID)
}

// minimizeAgent hides an agent's tab while every piece of its state stays
// alive: the orchestrator keeps running, the conversation, panes, scroll
// offsets, and session deck are retained, and the agent is listed in the
// Agents panel so one click brings it back. Closing a tab must never kill
// work in flight — terminating is the explicit cleanupAgent path.
func (m *App) minimizeAgent(agentID string) {
	if agentID == "" || m.AgentMgr == nil || m.AgentMgr.Get(agentID) == nil {
		return
	}
	ids := agentSubtreeIDs(m.AgentMgr.Get(agentID))
	m.MinimizeAgent(agentID)
	for _, id := range ids {
		if m.Win == nil {
			break
		}
		if tab := m.Win.TabByTitle(m.agentTabTitle(id)); tab != nil {
			m.Win.CloseTabByID(tab.ID)
		}
	}
	m.refreshActivityList()
}

// terminateAgent is the explicit destructive close: it cancels the running
// turn, stops the orchestrator, and drops every buffer. Only user-invoked
// "Terminate" actions reach it — closing a tab minimizes instead.
func (m *App) terminateAgent(agentID string) tea.Cmd {
	if agentID == "" || m.AgentMgr == nil || m.AgentMgr.Get(agentID) == nil {
		return nil
	}
	m.cleanupAgent(agentID)
	m.handleTabSwitch()
	m.refreshActivityList()
	if m.AgentMgr.VisibleCount() == 0 && m.EditorState.ActiveView == "" {
		m.openLauncher()
		m.LastInteractionTime = time.Now()
	}
	return nil
}

// restoreAgent re-surfaces a minimized agent and focuses its tab.
func (m *App) restoreAgent(agentID string) {
	if agentID == "" || m.AgentMgr == nil || m.AgentMgr.Get(agentID) == nil {
		return
	}
	// The launcher only exists while nothing is surfaced; a restored agent
	// takes the main content area back.
	m.launcherScreen = nil
	m.switchToAgentView(agentID)
	m.refreshActivityList()
}

// cleanupAgent removes all state for a given agent orchestrator or sub-agent.
// Removing an orchestrator is a subtree operation: every descendant tab,
// pane, projection, scroll snapshot, and status entry must disappear in the
// same update as the AgentManager tree. Otherwise the status bar can report a
// sleeping child after the Agents panel has already lost its parent tree.
func (m *App) cleanupAgent(agentID string) {
	ids := []string{agentID}
	if m.AgentMgr != nil {
		if node := m.AgentMgr.Get(agentID); node != nil {
			ids = agentSubtreeIDs(node)
		}
	}

	// Workspace.CloseAgent needs the tree and session ownership maps intact
	// to route backend cancellation and persist a recoverable session row.
	m.CloseAgent(agentID)
	if m.AgentMgr != nil {
		m.AgentMgr.Remove(agentID)
	}

	for _, id := range ids {
		m.clearAgentChatScroll(id)
		if m.subPaneProjections != nil {
			m.subPaneProjections.Remove(id)
		}
		if m.PaneMgr != nil {
			m.PaneMgr.Remove(id)
		}
		if m.Feedback != nil {
			if m.Status != nil {
				m.Status.RemoveAgent(id)
			}
		}
		if m.Win != nil {
			if tab := m.Win.TabByTitle(m.agentTabTitle(id)); tab != nil {
				m.Win.CloseTabByID(tab.ID)
			}
		}
	}
	m.syncPendingBtwCount()
}

func agentSubtreeIDs(root *workspace.AgentNode) []string {
	if root == nil {
		return nil
	}
	ids := make([]string, 0, 1+len(root.Children))
	var appendTree func(*workspace.AgentNode)
	appendTree = func(node *workspace.AgentNode) {
		if node == nil {
			return
		}
		ids = append(ids, node.ID)
		for _, child := range node.Children {
			appendTree(child)
		}
	}
	appendTree(root)
	return ids
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
	// Completion archives the tab but retains every pane/message for review.
	// The Agents panel reopens it without restarting execution.
	m.MinimizeAgent(agentID)

	if sp := m.PaneMgr.Get(agentID); sp != nil {
		sp.Status = n.State.Status
		if sp.Status == "" || sp.Status == "running" {
			sp.Status = "completed"
		}
		if !sp.Done {
			sp.Collapse()
		}
		if parentID != "" {
			m.subPaneProjections.InvalidateSubPane(parentID, agentID)
		}
	}

	n.State.Completed = true
	n.State.Streaming = false
	n.State.Thinking = false
	n.State.Running = false
	n.State.Cancelling = false
	n.State.PendingBlockID = ""
	if n.State.Status == "" || n.State.Status == "running" {
		n.State.Status = "completed"
	}

	if m.Status != nil {
		m.Status.RemoveAgent(agentID)
	}

	if !m.Win.IsOpen() {
		m.setMode(ModeChat)
	}
	m.handleTabSwitch()
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
	if agentID == "" {
		m.AgentMgr.ClearActive()
	} else if n := m.AgentMgr.Get(agentID); n != nil && n.State != nil {
		// Selecting a minimized agent is how the user brings it back.
		if n.IsHidden() {
			m.RestoreAgent(agentID)
		} else {
			m.AgentMgr.SetActive(agentID)
			m.ActivateSessionByAgent(agentID)
			m.OverlayState.SetYolo(m.YoloForAgent(agentID))
		}
	}

	curr := m.AgentMgr.ActiveID()
	m.syncAgentWindowTabs()
	if curr != "" {
		m.activateAgentWindowTab(curr)
	}
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

// activateAgentWindowTab makes agentID the selected window tab and the
// painted main view. Explicit user actions (New Agent, restore, existing
// orchestrator re-entry) must call this even when an editor tab is open —
// otherwise the session exists only in AgentMgr until every editor tab is
// closed. Background syncAgentWindowTabs must not call it.
func (m *App) activateAgentWindowTab(agentID string) {
	if agentID == "" || m.Win == nil {
		return
	}
	title := m.agentTabTitle(agentID)
	tab := m.Win.TabByTitle(title)
	if tab == nil {
		return
	}
	m.launcherScreen = nil
	if m.FocusStack != nil {
		m.FocusStack.Clear()
	}
	idx := m.Win.IndexOf(tab.ID)
	if m.Win.ActiveIndex() != idx {
		m.Win.SwitchTo(idx)
	}
	m.handleTabSwitch()
}

func (m *App) keepWindowTab(title string) {
	if title == "" || m.Win == nil {
		return
	}
	tab := m.Win.TabByTitle(title)
	if tab == nil {
		return
	}
	idx := m.Win.IndexOf(tab.ID)
	if m.Win.ActiveIndex() != idx {
		m.Win.SwitchTo(idx)
	}
}

// syncAgentWindowTabs creates/removes window tabs to match the current agent set.
// Every visible agent gets its own tab, including while an editor is the
// painted view — skipping creation there was the New Agent-while-editing bug.
//
// This function is structural only: it never chooses a tab. The Window's
// selected tab is preserved, then handleTabSwitch derives every active view
// and prompt target from that one authority. Explicit user actions call
// activateAgentWindowTab separately.
func (m *App) syncAgentWindowTabs() {
	preserveTitle := ""
	if active := m.Win.ActiveTab(); active != nil {
		preserveTitle = active.Title
	}

	// If no surfaced agents exist, purge agent tabs and return. Minimized
	// agents are still in the manager but own no tab.
	if m.AgentMgr.VisibleCount() == 0 {
		m.purgeAgentTabs()
		m.EditorState.TabSnapshotCache = ""
		m.keepWindowTab(preserveTitle)
		m.handleTabSwitch()
		return
	}

	tabViews := m.AgentTabViews()
	viewByID := make(map[string]workspace.TabView, len(tabViews))
	for _, view := range tabViews {
		viewByID[view.ID] = view
	}

	// Every visible agent in the canonical manager order owns one tab. Pane
	// creation and tab creation must not be separate authorities: that split
	// previously left selectable agents without tabs and stale tabs without a
	// prompt target.
	for _, id := range m.AgentMgr.TabOrder() {
		n := m.AgentMgr.Get(id)
		if n == nil || n.IsHidden() {
			continue
		}
		view, ok := viewByID[id]
		if !ok {
			continue
		}
		title := m.agentTabTitle(id)
		if m.Win.TabByTitle(title) == nil {
			m.Win.OpenTabInactive(title, "", true)
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

	m.purgeStaleAgentTabs()
	// Root first within each agent tree, including a resumed session whose
	// child tabs were opened before its root. Keep editor slots where they are.
	order := m.AgentMgr.TabOrder()
	ids := make([]window.TabID, 0, len(order))
	for _, id := range order {
		if tab := m.Win.TabByTitle(m.agentTabTitle(id)); tab != nil {
			ids = append(ids, tab.ID)
		}
	}
	m.Win.OrderTabsSubset(ids)
	m.keepWindowTab(preserveTitle)
	m.handleTabSwitch()
}

func (m *App) activeAgentTabID() string {
	if m == nil || m.Win == nil {
		return ""
	}
	tab := m.Win.ActiveTab()
	if tab == nil || !m.isAgentTabTitle(tab.Title) {
		return ""
	}
	id := m.agentIDFromTabTitle(tab.Title)
	if node := m.AgentMgr.Get(id); node != nil && !node.IsHidden() {
		return id
	}
	return ""
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

// purgeStaleAgentTabs closes agent tabs for agents that no longer exist or
// were minimized. Completed agents remain valid review surfaces when reopened.
func (m *App) purgeStaleAgentTabs() {
	for i := m.Win.TabCount() - 1; i >= 0; i-- {
		tabs := m.Win.Tabs()
		if i >= len(tabs) {
			continue
		}
		if m.isAgentTabTitle(tabs[i].Title) {
			agentID := m.agentIDFromTabTitle(tabs[i].Title)
			n := m.AgentMgr.Get(agentID)
			if n == nil || n.IsHidden() {
				m.Win.CloseTabByID(tabs[i].ID)
			}
		}
	}
}
