package viewmodel

import (
	"strings"

	"crude/provider"
	"crude/tui/ui"
	"crude/tui/widgets"
	"crude/workspace"
)

func (m *App) AddOrchestrator(orchID string, inputCh chan<- provider.UserInput, btwCh chan<- provider.UserInput, cancelCh chan<- struct{}, blockResolveCh chan string) {
	m.launcher = nil
	if m.AgentMgr.Get(orchID) != nil {
		m.switchToAgentView(orchID)
		return
	}

	ml := widgets.NewMessageList(m.Theme)
	ml.SetModelName(m.ModelName)
	ml.SetUsername(m.Username)
	ml.SetSubPaneContentFn(m.renderSubPaneContent)

	sp := m.PaneMgr.GetOrCreate(orchID, workspace.PaneAgent, orchID, m.Theme)
	sp.MsgList = ml
	sp.MsgList.SetModelName(m.ModelName)
	sp.MsgList.SetUsername(m.Username)

	agent := workspace.NewAgentState(orchID, sp.Messages, m.Logger)
	m.AgentMgr.AddOrchestrator(orchID, inputCh, btwCh, cancelCh, blockResolveCh, agent)
	m.syncAgentWindowTabs()
}

func (m *App) RemoveOrchestrator(orchID string) {
	m.cleanupAgent(orchID)
}

// cleanupAgent removes all state for a given agent orchestrator or sub-agent.
func (m *App) cleanupAgent(agentID string) {
	m.CloseAgent(agentID)
	m.AgentMgr.Remove(agentID)
	m.PaneMgr.Remove(agentID)
	if m.StatusBar != nil {
		m.StatusBar.ClearAgentState(agentID)
	}
}

func (m *App) activeMsgList() *widgets.MessageList {
	n := m.AgentMgr.ActiveNode()
	if n == nil {
		return nil
	}
	if sp := m.PaneMgr.Get(n.ID); sp != nil {
		return sp.MsgList
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
			if parentSP := m.PaneMgr.Get(parentID); parentSP != nil {
				parentSP.MsgList.InvalidateAllMessages()
			}
		}
	}

	if sp := m.PaneMgr.Get(agentID); sp != nil && !sp.Done {
		sp.Collapse()
		if parentSP := m.PaneMgr.Get(parentID); parentSP != nil {
			parentSP.MsgList.InvalidateSubPane(agentID)
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
	} else {
		m.syncOverlayMode()
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
		ml.ClearSelection()
		ml.InvalidateAllMessages()
	}
	switch newMode {
	case ModeSettings:
		m.Input.ClearInput()
		m.Overlay.OpenTab("Session Settings", "", true)
	case ModeConfirm:
		m.Overlay.OpenTab("Confirm", "", true)
	case ModeQuestion:
		m.Overlay.OpenTab("Question", "", true)
	case ModeYolo:
		m.Overlay.OpenTab("YOLO", "", true)
	case ModeTextOverlay:
		m.openTextOverlayWindow()
	case ModeFilePicker:
		m.openFilePickerWindow()
	case ModeChat:
		m.FormHint = ""
		m.overlayHost.Close(ui.OverlayTextOverlay)
		m.overlayHost.Close(ui.OverlayFilePicker)
	}
}

func (m *App) activeMessageList() *widgets.MessageList {
	activeML := m.activeMsgList()
	if activeML != nil {
		return activeML
	}
	// Fallback: use any available agent's SubPane MsgList
	for _, n := range m.AgentMgr.AllNodes() {
		if sp := m.PaneMgr.Get(n.ID); sp != nil {
			return sp.MsgList
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

func (m *App) activeAgentID() string {
	return m.AgentMgr.ActiveID()
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
			if pane := m.PaneMgr.Get(prev); pane != nil && pane.MsgList != nil {
				pane.MsgList.InvalidateAllMessages()
			}
		}
		if curr != "" {
			if pane := m.PaneMgr.Get(curr); pane != nil && pane.MsgList != nil {
				pane.MsgList.InvalidateAllMessages()
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

	// Create tabs for sub-agents that have panes
	for _, id := range m.PaneMgr.PaneOrder() {
		n := m.AgentMgr.Get(id)
		if n == nil || (n.State != nil && n.State.Completed) {
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
			displayLabel := id
			if n.Label != "" {
				displayLabel = n.Label
			}
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
		title := m.agentTabTitle(id)
		if m.Win.TabByTitle(title) == nil {
			m.Win.OpenTab(title, "", true)
		}
		if tab := m.Win.TabByTitle(title); tab != nil {
			newIcon := m.agentTabIcon(id)
			if tab.Icon != newIcon {
				m.Win.SetTabIconByIndex(m.Win.IndexOf(tab.ID), newIcon)
			}
			displayLabel := id
			if n.Label != "" {
				displayLabel = n.Label
			}
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

func (m *App) agentTabSnapshot() string {
	var b strings.Builder
	for _, id := range m.PaneMgr.PaneOrder() {
		n := m.AgentMgr.Get(id)
		if n == nil || n.State == nil || n.State.Completed {
			continue
		}
		b.WriteString(id)
		b.WriteByte('|')
		b.WriteString(n.State.Status)
		b.WriteByte('|')
		if n.Label != "" {
			b.WriteString(n.Label)
		}
		b.WriteByte('|')
	}
	return b.String()
}

func (m *App) ensureAgentTabActive() {
	activeID := m.AgentMgr.ActiveID()
	if activeID == "" {
		return
	}
	activeTitle := m.agentTabTitle(activeID)
	if tab := m.Win.TabByTitle(activeTitle); tab != nil {
		targetIdx := m.Win.IndexOf(tab.ID)
		if targetIdx >= 0 && m.Win.ActiveIndex() != targetIdx {
			m.Win.SwitchTo(targetIdx)
		}
	}
}

// agentTabIcon returns the status icon for an agent's window tab.
func (m *App) agentTabIcon(id string) string {
	if id == "" {
		return "■"
	}
	n := m.AgentMgr.Get(id)
	if n == nil || n.State == nil || n.State.Status == "" {
		return "●"
	}
	status := n.State.Status
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

// closeAllOverlayTabs closes all overlay tabs and returns to chat mode.
func (m *App) closeAllOverlayTabs() {
	m.Overlay.CloseAll()
	if m.OverlayState.IsOverlay() {
		m.OverlayState.Set(ModeChat)
		m.FormHint = ""
		m.Input.ResetPrompt()
		m.Input.ClearInput()
	}
}

func (m *App) syncOverlayMode() {
	if !m.Overlay.IsOpen() {
		if m.OverlayState.IsOverlay() {
			m.OverlayState.Set(ModeChat)
			m.FormHint = ""
			m.Input.ResetPrompt()
			m.Input.ClearInput()
		}
		return
	}
	activeTab := m.Overlay.ActiveTab()
	if activeTab == nil {
		if m.OverlayState.IsOverlay() {
			m.OverlayState.Set(ModeChat)
			m.FormHint = ""
			m.Input.ResetPrompt()
			m.Input.ClearInput()
		}
		return
	}
	switch {
	case activeTab.Title == "Session Settings":
		m.OverlayState.Set(ModeSettings)
	case activeTab.Title == "Confirm":
		m.OverlayState.Set(ModeConfirm)
	case activeTab.Title == "YOLO":
		m.OverlayState.Set(ModeYolo)
	case activeTab.Title == "Question":
		m.OverlayState.Set(ModeQuestion)
	default:
		m.OverlayState.Set(ModeChat)
		m.FormHint = ""
	}
}

func (m *App) renderSubPaneContent(paneID string, width, maxH, spinFrame int) []string {
	sp := m.PaneMgr.Get(paneID)
	if sp == nil {
		return []string{"", "● unknown pane"}
	}
	if n := m.AgentMgr.Get(paneID); n != nil && n.State != nil {
		sp.MsgList.SetStreaming(n.State.Streaming)
		sp.MsgList.SetActiveTyping(n.State.ComputeActiveTyping())
	}
	result := sp.Render(width, maxH, spinFrame)
	return result.Lines
}
