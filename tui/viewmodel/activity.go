package viewmodel

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/backgroundjobs"
	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/screens/panelchrome"
	"crude/tui/screens/shelltree"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	captypes "crude/types/capability"
	"crude/workspace"
)

const (
	activityFiles  = activitybar.ViewFiles
	activityAgents = activitybar.ViewAgents
	activitySkills = activitybar.ViewSkills
	activityMCP    = activitybar.ViewMCP

	sidebarItemNewAgent = "agent.new"
)

func (m *App) activityChromeWidth() int {
	if m == nil || m.activityBar == nil {
		return 0
	}
	return activitybar.Width
}

func (m *App) sidebarHitWidth() int {
	w := m.frameLeftCol() + m.activityChromeWidth()
	if m.sidebarHostVisible() && m.Tree != nil {
		w += m.sidebarPanelWidth() + 1
	}
	return w
}

func (m *App) sidebarPanelWidth() int {
	if m == nil || m.Tree == nil {
		return 0
	}
	width := m.Tree.Width
	if m.width > 0 {
		width = min(width, m.width/2)
	}
	return max(0, width)
}

func (m *App) setActivity(view activitybar.View, showContent bool) {
	if view != m.activityView {
		m.captureToolView()
	}
	if m.activityBar != nil {
		m.activityBar.SetActive(view)
	}
	m.activityView = view
	if m.Tree != nil {
		if showContent {
			m.Tree.Visible = true
		}
	}
	m.refreshActivityList()
	if state, exists := m.toolLayout.Views[view.ID()]; exists {
		if state.Width > 0 {
			m.Tree.Width = state.Width
		}
		if m.activityList != nil && (view == activityAgents || view == activitySkills || view == activityMCP) {
			m.activityList.RestoreViewState(activitylist.ViewState{Filter: state.Filter, Selection: state.Selection, Offset: state.Offset})
		}
	} else if m.activityList != nil && (view == activityAgents || view == activitySkills || view == activityMCP) {
		m.activityList.RestoreViewState(activitylist.ViewState{})
	}
}

func (m *App) toggleActivity(view activitybar.View) tea.Cmd {
	if m.activityView == view && m.Tree != nil && m.Tree.Visible && m.focusTarget() == FocusTree {
		m.Tree.Visible = false
		if m.FocusStack != nil && m.FocusStack.Top() == FocusTree {
			m.FocusStack.Pop()
		}
		return nil
	}
	m.setActivity(view, true)
	if m.FocusStack != nil {
		m.FocusStack.Clear()
		m.FocusStack.Push(FocusTree)
	}
	if view == activitySkills || view == activityMCP {
		return m.capabilitySnapshotCmd()
	}
	return nil
}

func (m *App) refreshActivityList() {
	if m.activityBar != nil {
		// The rail's accent marker tracks whether the panel is actually
		// mounted, so a collapsed sidebar reads as collapsed.
		m.activityBar.SetPanelOpen(m.sidebarContentVisible())
	}
	if m.activityList == nil {
		return
	}
	if m.activityBar != nil {
		if m.ws.AgentMgr() != nil {
			// Counts every agent the panel lists, minimized included —
			// that badge is how a backgrounded agent stays discoverable.
			m.activityBar.SetBadge(activityAgents, m.ws.AgentMgr().Count())
		}
	}
	nerd := panelchrome.NerdFont(m.Theme)
	switch m.activityView {
	case activityAgents:
		m.activityList.SetHeading("Agents")
		m.activityList.SetIcon(activitybar.ViewAgents.Icon(nerd))
		items := m.agentSidebarItems()
		m.activityList.SetBadge(sidebarCountBadge(items))
		m.activityList.SetItems(items)
	case activitySkills, activityMCP:
		m.activityList.SetHeading(m.activityView.Label())
		m.activityList.SetIcon(m.activityView.Icon(nerd))
		items := m.capabilitySidebarItems(m.activityView)
		m.activityList.SetBadge(sidebarCountBadge(items))
		m.activityList.SetItems(items)
	default:
		m.activityList.SetHeading("Explorer")
		m.activityList.SetIcon(activitybar.ViewFiles.Icon(nerd))
		m.activityList.SetBadge("")
	}
}

func (m *App) agentSidebarItems() []ui.ListItem {
	items := []ui.ListItem{{
		ID:      sidebarItemNewAgent,
		Label:   "New Agent",
		Icon:    "+",
		Command: true,
	}}
	if m.ws.AgentMgr() == nil {
		return append(items, ui.ListItem{ID: "none", Label: "No agents", Disabled: true})
	}
	roots := m.ws.AgentMgr().Orchestrators()
	if len(roots) == 0 {
		return append(items, ui.ListItem{ID: "none", Label: "No agents", Disabled: true})
	}
	var appendTree func(*workspace.AgentNode, int, bool)
	appendTree = func(n *workspace.AgentNode, depth int, last bool) {
		if n == nil {
			return
		}
		label := workspace.AgentDisplayLabel(n)
		if label == "" {
			if n.IsOrchestrator() {
				label = "Main Agent"
			} else {
				label = "Subagent"
			}
		}
		detail := ""
		icon := "◇"
		if n.IsOrchestrator() {
			icon = "◆"
		}
		switch {
		case n.IsActive():
			detail = "running"
			icon = "●"
		case n.IsCompleted():
			icon = "✓"
		}
		// Minimized agents stay listed here: this panel is how they come
		// back, so the row has to say the tab is gone, not that the agent is.
		if m.AgentView.IsHidden(n.ID) {
			if n.IsCompleted() {
				icon = "✓"
			} else if detail == "running" {
				detail = "running · minimized"
			} else {
				detail = "minimized"
				icon = "▪"
			}
		}
		items = append(items, ui.ListItem{
			ID:       "agent:" + n.ID,
			Label:    label,
			Detail:   detail,
			Icon:     icon,
			Depth:    depth,
			TreeLast: last,
		})
		for i, child := range n.Children {
			appendTree(child, depth+1, i == len(n.Children)-1)
		}
	}
	for i, root := range roots {
		appendTree(root, 0, i == len(roots)-1)
	}
	return items
}

func sidebarCountBadge(items []ui.ListItem) string {
	count := 0
	for _, item := range items {
		if item.Selectable() && !item.Command {
			count++
		}
	}
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("%d", count)
}

func (m *App) handleSidebarActivate(item ui.ListItem) tea.Cmd {
	switch {
	case strings.HasPrefix(item.ID, "capability:"):
		return m.openCapabilityManager(m.activityView, "", strings.TrimPrefix(item.ID, "capability:"))
	case item.ID == "capability.manage":
		return m.openCapabilityManager(m.activityView, "", "")
	case item.ID == sidebarItemNewAgent:
		return m.launcherNewSession()
	case strings.HasPrefix(item.ID, "agent:"):
		m.restoreAgent(strings.TrimPrefix(item.ID, "agent:"))
		return nil
	}
	return nil
}

func (m *App) handleHostAction(action *ui.Action) tea.Cmd {
	if action == nil {
		return nil
	}
	if cmd, handled := m.handleMainPaneAction(action); handled {
		return cmd
	}
	if m.handleTerminalAction(*action) {
		return nil
	}
	if cmd, handled := m.handleGitPanelAction(*action); handled {
		return cmd
	}
	if cmd, handled := m.handleLocalDiffAction(*action); handled {
		return cmd
	}
	if cmd, handled := m.handleBoardAction(*action); handled {
		return cmd
	}
	if cmd, handled := m.handleCapabilityPickerAction(*action); handled {
		return cmd
	}
	switch action.ID {
	case "agent-prompt.close":
		id, _ := action.Payload.(string)
		m.closeAgentPromptTab(id)
		return nil
	case backgroundjobs.ActionBlur:
		m.setSessionDeckFocused(false)
		return nil
	case backgroundjobs.ActionTerminate:
		request, ok := action.Payload.(backgroundjobs.TerminateRequest)
		if ok {
			return m.terminateBackgroundJob(request)
		}
		return nil
	case "skills.manage":
		return m.openCapabilityManager(activitySkills, "", "")
	case "capabilities.pick":
		return m.openCapabilityPicker()
	case "capabilities.pick.mcp":
		return m.openCapabilityPickerTab(captypes.KindMCP, "")
	case "mcp.manage":
		return m.openCapabilityManager(activityMCP, "", "")
	case "workspace.panel.maximize":
		m.toggleWorkspacePanelMaximized()
		return nil
	case "workspace.panel.hide":
		m.hideWorkspacePanel()
		return nil
	case "workspace.panel.more":
		m.openFooterMenu()
		return nil
	case "terminal.picker":
		m.openTerminalPicker()
		return nil
	case "input.attachment.open":
		id, _ := action.Payload.(string)
		return m.openAttachment(id, false)
	case "input.attachment.remove":
		id, _ := action.Payload.(string)
		for _, rr := range m.Input.RefRanges() {
			if rr.ID == id {
				m.removeAttachment(rr)
				break
			}
		}
		m.persistComposerDraftIfChanged()
		return nil
	case historyscreen.ActionPreview:
		request, _ := action.Payload.(historyscreen.PreviewRequest)
		return m.historyPreviewCmd(request)
	case historyscreen.ActionApply:
		request, _ := action.Payload.(historyscreen.ApplyRequest)
		return m.historyApplyCmd(request)
	case historyscreen.ActionBack:
		return m.openLocalHistory()
	case ui.ActionTextPaste:
		target, _ := action.Payload.(ui.ClipboardPasteTarget)
		return m.beginNativeClipboardPaste(target, "")
	case termdock.ActionNew:
		return m.openNewTerminal()
	case activitybar.ActionSelect:
		view, ok := action.Payload.(activitybar.View)
		if ok {
			return m.toggleActivity(view)
		}
		return nil
	case activitybar.ActionSettings:
		m.openApplicationSettings()
		return nil
	case activitybar.ActionOverflow:
		m.openNavigationMenu()
		return nil
	case activitylist.ActionActivate:
		item, ok := action.Payload.(ui.ListItem)
		if ok {
			return m.handleSidebarActivate(item)
		}
	case chatprojection.ChatRecoveryRetryActionID:
		payload, ok := action.Payload.(chatprojection.ChatRecoveryRetryPayload)
		if !ok || m.ws.AgentMgr() == nil {
			return nil
		}
		node := m.ws.AgentMgr().Get(payload.AgentID)
		if node == nil || node.State == nil {
			return nil
		}
		status := node.State.RecoveryStatus()
		if status.ID != payload.IncidentID || status.Generation != payload.Generation {
			return nil
		}
		return m.retryAgentRecovery(payload.AgentID)
	case chatprojection.ChatToggleActionID:
		payload, ok := action.Payload.(chatprojection.ChatTogglePayload)
		if !ok {
			return nil
		}
		if ml := m.activeMessageList(); ml != nil {
			ml.ToggleDisclosure(payload.MessageID)
		}
		return nil
	case ui.ActionSplitResize:
		payload, ok := action.Payload.(ui.SplitResize)
		if !ok {
			return nil
		}
		if payload.ID == activitylist.DividerID || payload.ID == shelltree.DividerID {
			m.setSidebarWidth(payload.First)
			m.captureToolView()
			m.toolLayoutVersion++
		}
		if payload.ID == termdock.DividerID {
			m.setTermDockHeight((m.height - m.chromeBottomRows()) - payload.First)
		}
	}
	return nil
}
