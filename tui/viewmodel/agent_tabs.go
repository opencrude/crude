package viewmodel

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/chatprojection"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

func (m *App) AddOrchestrator(orchID string) {
	if _, remote := m.ws.(shellport.Replica); remote && m.ws.AgentMgr().Get(orchID) == nil {
		if m.pendingOrchestrators == nil {
			m.pendingOrchestrators = make(map[string]bool)
		}
		m.pendingOrchestrators[orchID] = true
		return
	}
	m.addOrchestrator(orchID, true)
}

// addOrchestrator surfaces inventory without allowing peer-created roots to
// steal this shell's selection or editor focus.
func (m *App) addOrchestrator(orchID string, activate bool) {
	if !activate {
		m.ensureAgentProjection(orchID)
		for _, childID := range agentSubtreeIDs(m.ws.AgentMgr().Get(orchID)) {
			if childID == orchID {
				continue
			}
			if node := m.ws.AgentMgr().Get(childID); node != nil && node.State != nil && node.State.Completed && m.subPaneProjections.GetExisting(childID) == nil {
				m.MinimizeAgent(childID)
			}
			m.ensureAgentProjection(childID)
			m.syncAgentMessages(childID, false)
		}
		m.syncAgentMessages(orchID, false)
		m.syncAgentWindowTabs()
		return
	}
	if m.rootHost != nil {
		m.rootHost.CancelPointerCapture()
	}
	m.launcherScreen = nil
	m.ActivateSessionByAgent(orchID)
	m.ws.RegisterAgentRoot(orchID)
	m.ensureAgentProjection(orchID)
	m.ws.HydrateSubAgents(orchID)
	// Cold lifecycle preparation may have installed child inventory before the
	// shell sees it. Only unseen completed review children default to minimized;
	// reopening a warm root preserves this shell's deliberate child choices.
	for _, childID := range agentSubtreeIDs(m.ws.AgentMgr().Get(orchID)) {
		if childID == orchID {
			continue
		}
		node := m.ws.AgentMgr().Get(childID)
		if node != nil && node.State != nil && node.State.Completed && m.subPaneProjections.GetExisting(childID) == nil {
			m.MinimizeAgent(childID)
		}
		m.ensureAgentProjection(childID)
		m.syncAgentMessages(childID, false)
	}
	m.syncAgentMessages(orchID, false)
	m.syncAgentStatusInventory()
	m.RestoreAgent(orchID)
	m.syncAgentWindowTabs()
	m.activateAgentWindowTab(orchID)
	m.offerGoalResume(orchID)
}

// ensureAgentProjection binds a local renderer to existing canonical inventory.
// It never constructs another AgentState or replaces a live conversation.
func (m *App) ensureAgentProjection(id string) {
	node := m.ws.AgentMgr().Get(id)
	if node == nil || node.State == nil || m.subPaneProjections == nil {
		return
	}
	projection := m.subPaneProjections.GetExisting(id)
	if projection == nil {
		projection = m.subPaneProjections.Get(id, workspace.PaneAgent)
		projection.renderer.SetSubPaneContentFn(m.renderSubPaneContentForApp)
	}
	projection.renderer.SetUsername(m.ws.Username())
}

func (m *App) RemoveOrchestrator(orchID string) {
	m.cleanupAgent(orchID)
}

// minimizeAgent hides an agent's tab while every piece of its state stays
// alive: the orchestrator keeps running, the conversation, panes, scroll
// offsets, and session deck are retained, and the agent is listed in the
// Agents panel so one click brings it back. Closing a tab must never kill
// work in flight; terminating is the explicit cleanupAgent path.
func (m *App) minimizeAgent(agentID string) {
	if agentID == "" || m.ws.AgentMgr() == nil || m.ws.AgentMgr().Get(agentID) == nil {
		return
	}
	ids := agentSubtreeIDs(m.ws.AgentMgr().Get(agentID))
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
// "Terminate" actions reach it; closing a tab minimizes instead.
func (m *App) terminateAgent(agentID string) tea.Cmd {
	if agentID == "" || m.ws.AgentMgr() == nil || m.ws.AgentMgr().Get(agentID) == nil {
		return nil
	}
	defer m.releaseTabBarFocus()
	m.cleanupAgent(agentID)
	m.handleTabSwitch()
	m.refreshActivityList()
	if m.AgentView.VisibleCount() == 0 && m.EditorState.ActiveView == "" {
		m.openLauncher()
		m.LastInteractionTime = time.Now()
	}
	return nil
}

// restoreAgent re-surfaces a minimized agent and focuses its tab.
func (m *App) restoreAgent(agentID string) {
	if agentID == "" || m.ws.AgentMgr() == nil || m.ws.AgentMgr().Get(agentID) == nil {
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
	if m.ws.AgentMgr() != nil {
		if node := m.ws.AgentMgr().Get(agentID); node != nil {
			ids = agentSubtreeIDs(node)
		}
	}
	for intent := range m.pendingInputAdmissions {
		for _, id := range ids {
			if intent.AgentID == id {
				delete(m.pendingInputAdmissions, intent)
				break
			}
		}
	}

	// Workspace.CloseAgent needs the tree and session ownership maps intact
	// to route backend cancellation and persist a recoverable session row.
	for _, state := range append([]*agentPromptState(nil), m.agentPrompts...) {
		for _, id := range ids {
			if state.request.agentID == id {
				m.removeAgentPrompt(state)
				break
			}
		}
	}
	for _, id := range ids {
		if node := m.ws.AgentMgr().Get(id); node != nil && node.State != nil {
			m.clearThinkingPresentationOwner(node.State)
		}
	}
	m.ws.CloseAgent(agentID)
	if m.ws.AgentMgr() != nil {
		m.AgentView.forgetSubtree(m.ws.AgentMgr().Get(agentID))
		m.ws.ForgetAgentInventory(agentID)
	}

	for _, id := range ids {
		if m.mainPanes != nil {
			if view := m.mainPanes.agentViews[id]; view != nil {
				for tabID, candidate := range m.mainPanes.views {
					if candidate == view {
						delete(m.mainPanes.views, tabID)
					}
				}
				delete(m.mainPanes.agentViews, id)
			}
		}
		if m.subPaneProjections != nil {
			m.subPaneProjections.Remove(id)
		}
		if m.Win != nil {
			if tab := m.Win.TabByTitle(m.agentTabTitle(id)); tab != nil {
				m.Win.CloseTabByID(tab.ID)
			}
		}
	}
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
	n := m.AgentView.ActiveNode()
	if n == nil {
		return nil
	}
	if p := m.subPaneProjections.GetExisting(n.ID); p != nil {
		return p.renderer
	}
	return nil
}

func (m *App) completeSubAgent(agentID string) {
	node := m.ws.AgentMgr().Get(agentID)
	if node == nil || node.State == nil || node.Parent == nil {
		return
	}
	status := node.State.Status
	if status == "" || status == "running" {
		status = "completed"
	}
	change := m.ws.ApplyAgentEvent(workspace.AgentEvent{AgentID: agentID, Type: "sub_agent_completed", Data: map[string]string{"agent_id": agentID, "status": status}})
	if change.Accepted {
		m.archiveSubAgentTab(agentID)
	}
}

// archiveSubAgentTab is this shell's completion policy, not runtime settlement.
func (m *App) archiveSubAgentTab(agentID string) {
	node := m.ws.AgentMgr().Get(agentID)
	if node == nil {
		return
	}
	for _, id := range agentSubtreeIDs(node) {
		m.dismissAgentPromptFor(id)
	}
	if tab := m.Win.TabByTitle(m.agentTabTitle(agentID)); tab != nil {
		m.Win.CloseTabByID(tab.ID)
	}
	m.MinimizeAgent(agentID)
	if node.Parent != nil {
		m.subPaneProjections.InvalidateSubPane(node.Parent.ID, agentID)
	}
	if !m.Win.IsOpen() {
		m.setMode(ModeChat)
	}
	m.handleTabSwitch()
}

// anyActive reports agent work in progress. Agent state is the only owner:
// every agent has a mirrored pane, and a root agent's pane never collapses,
// so pane completion is not a liveness signal.
func (m *App) anyActive() bool {
	return m.ws.AgentMgr().AnyActive()
}

// anyAgentAnimating adds turns that are stopping, whose live status still moves.
func (m *App) anyAgentAnimating() bool {
	for _, node := range m.ws.AgentMgr().AllNodes() {
		if node != nil && node.State != nil && (node.State.IsActive() || node.State.Cancelling) {
			return true
		}
	}
	return false
}

func (m *App) setMode(newMode Mode) {
	oldMode := m.OverlayState.Current()
	if m.ws.Logger() != nil {
		m.ws.Logger().Debug("UI", "mode_switch", "from", modeName(oldMode), "to", modeName(newMode))
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
	for _, n := range m.ws.AgentMgr().AllNodes() {
		if p := m.subPaneProjections.GetExisting(n.ID); p != nil {
			return p.renderer
		}
	}
	return nil
}

func (m *App) agentForView() *workspace.AgentState {
	if n := m.AgentView.ActiveNode(); n != nil && n.State != nil {
		return n.State
	}
	for _, n := range m.ws.AgentMgr().AllNodes() {
		if n.State != nil {
			return n.State
		}
	}
	return nil
}

func (m *App) switchToAgentView(agentID string) {
	if agentID == "" {
		m.AgentView.ClearActive()
	} else if n := m.ws.AgentMgr().Get(agentID); n != nil && n.State != nil {
		// Selecting a minimized agent is how the user brings it back.
		if m.AgentView.IsHidden(n.ID) {
			m.RestoreAgent(agentID)
		} else {
			m.AgentView.SetActive(agentID)
		}
		m.ActivateSessionByAgent(agentID)
		m.OverlayState.SetYolo(m.ws.YoloForAgent(agentID))
	}

	curr := m.AgentView.ActiveID()
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
// orchestrator re-entry) must call this even when an editor tab is open;
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
// painted view; skipping creation there was the New Agent-while-editing bug.
//
// This function is structural only: it never chooses a tab. The Window's
// selected tab is preserved, then handleTabSwitch derives every active view
// and prompt target from that one authority. Explicit user actions call
// activateAgentWindowTab separately.
func (m *App) syncAgentWindowTabs() {
	preserveGroup := m.Win.ActiveGroup()
	var added []window.TabID
	preserveTitle := ""
	if active := m.Win.ActiveTab(); active != nil {
		preserveTitle = active.Title
	}

	// If no surfaced agents exist, purge agent tabs and return. Minimized
	// agents are still in the manager but own no tab.
	if m.AgentView.VisibleCount() == 0 {
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
	for _, id := range m.AgentView.TabOrder() {
		n := m.ws.AgentMgr().Get(id)
		if n == nil || m.AgentView.IsHidden(n.ID) {
			continue
		}
		view, ok := viewByID[id]
		if !ok {
			continue
		}
		title := m.agentTabTitle(id)
		if m.Win.TabByTitle(title) == nil {
			// New background children follow their parent's group, not an
			// unrelated editor or deliberately empty pane the user is using.
			if n.Parent != nil {
				if parent := m.Win.TabByTitle(m.agentTabTitle(n.Parent.ID)); parent != nil {
					m.Win.FocusGroup(parent.Group)
				}
			}
			added = append(added, m.Win.OpenTabInactive(title, "", true).ID)
			m.Win.FocusGroup(preserveGroup)
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
	// Position newly discovered agents next to their family, without sorting
	// existing tabs back over a deliberate user reorder.
	order := m.AgentView.TabOrder()
	ids := make([]window.TabID, 0, len(order))
	for _, id := range order {
		if tab := m.Win.TabByTitle(m.agentTabTitle(id)); tab != nil {
			ids = append(ids, tab.ID)
		}
	}
	if len(added) > 0 {
		freshSet := make(map[window.TabID]bool, len(added))
		for _, id := range added {
			freshSet[id] = true
		}
		stable := make([]window.TabID, 0, len(ids))
		for _, tab := range m.Win.Tabs() {
			if m.isAgentTabTitle(tab.Title) && !freshSet[tab.ID] {
				stable = append(stable, tab.ID)
			}
		}
		for _, fresh := range added {
			insert := len(stable)
			for position, id := range ids {
				if id != fresh {
					continue
				}
				for previous := position - 1; previous >= 0; previous-- {
					found := false
					for index, old := range stable {
						if old == ids[previous] {
							insert = index + 1
							found = true
							break
						}
					}
					if found {
						break
					}
				}
				break
			}
			stable = append(stable, 0)
			copy(stable[insert+1:], stable[insert:])
			stable[insert] = fresh
		}
		m.Win.OrderTabsSubset(stable)
	}
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
	if node := m.ws.AgentMgr().Get(id); node != nil && !m.AgentView.IsHidden(node.ID) {
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
			n := m.ws.AgentMgr().Get(agentID)
			if n == nil || m.AgentView.IsHidden(n.ID) {
				m.Win.CloseTabByID(tabs[i].ID)
			}
		}
	}
}
