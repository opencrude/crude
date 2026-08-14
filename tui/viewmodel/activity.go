package viewmodel

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/panelchrome"
	"crude/tui/screens/shelltree"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	activityFiles     = activitybar.ViewFiles
	activityAgents    = activitybar.ViewAgents
	activityTerminals = activitybar.ViewTerminals
	activityGit       = activitybar.ViewGit

	sidebarItemNewTerminal = "terminal.new"
	sidebarItemNewAgent    = "agent.new"
	// Row 0 is the outer top pad; rows 1-2 belong to the global
	// application header. Sidebar chrome begins below that header so it
	// cannot paint over the Crude brand, model, usage, or elapsed clock.
	sidebarTopRow = 3
)

func (m *App) activityChromeWidth() int {
	if m == nil || m.activityBar == nil {
		return 0
	}
	return activitybar.Width
}

func (m *App) sidebarHitWidth() int {
	w := m.activityChromeWidth()
	if m.sidebarHostVisible() && m.Tree != nil {
		w += m.Tree.Width + 1
	}
	return w
}

func (m *App) setActivity(view activitybar.View, showContent bool) {
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
}

func (m *App) toggleActivity(view activitybar.View) tea.Cmd {
	if m.activityView == view && m.Tree != nil && m.Tree.Visible {
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
	if view == activityGit {
		return m.refreshSourceControl()
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
		if m.AgentMgr != nil {
			// Counts every agent the panel lists, minimized included —
			// that badge is how a backgrounded agent stays discoverable.
			m.activityBar.SetBadge(activityAgents, m.AgentMgr.Count())
		}
		if mgr := m.termMgr(); mgr != nil {
			m.activityBar.SetBadge(activityTerminals, len(mgr.List()))
		}
		if m.Tree != nil && m.Tree.Model != nil && m.Tree.Model.GitResolver() != nil {
			m.activityBar.SetBadge(activityGit, len(m.Tree.Model.GitResolver().AllStatuses()))
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
	case activityTerminals:
		m.activityList.SetHeading("Terminals")
		m.activityList.SetIcon(activitybar.ViewTerminals.Icon(nerd))
		items := m.terminalSidebarItems()
		m.activityList.SetBadge(sidebarCountBadge(items))
		m.activityList.SetItems(items)
	case activityGit:
		m.activityList.SetHeading("Source Control")
		m.activityList.SetIcon(activitybar.ViewGit.Icon(nerd))
		items := m.gitSidebarItems()
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
	if m.AgentMgr == nil {
		return append(items, ui.ListItem{ID: "none", Label: "No agents", Disabled: true})
	}
	roots := m.AgentMgr.Orchestrators()
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
		detail := n.Model
		icon := "◇"
		if n.IsOrchestrator() {
			icon = "◆"
		}
		switch {
		case n.IsActive():
			detail = "running"
			icon = "●"
		case n.IsCompleted():
			detail = "done"
			icon = "✓"
		case detail == "":
			detail = "idle"
		}
		// Minimized agents stay listed here: this panel is how they come
		// back, so the row has to say the tab is gone, not that the agent is.
		if n.Hidden {
			if detail == "running" {
				detail = "running · minimized"
			} else {
				detail = "minimized"
			}
			icon = "▪"
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

func (m *App) terminalSidebarItems() []ui.ListItem {
	items := []ui.ListItem{{
		ID:      sidebarItemNewTerminal,
		Label:   "New Terminal",
		Icon:    "+",
		Command: true,
	}}
	mgr := m.termMgr()
	if mgr == nil {
		return items
	}
	list := mgr.List()
	if len(list) == 0 {
		items = append(items, ui.ListItem{ID: "none", Label: "No open terminals", Disabled: true})
		return items
	}
	for _, t := range list {
		place := "docked"
		if t.Placement == workspace.TerminalFloating {
			place = "window"
		}
		if !t.Alive {
			place = "exited"
		}
		title := t.Title
		if title == "" {
			title = "Terminal"
		}
		items = append(items, ui.ListItem{
			ID:     "term:" + t.ID,
			Label:  title,
			Detail: place,
			Icon:   "›_",
		})
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

func (m *App) gitSidebarItems() []ui.ListItem {
	if m.Tree == nil || m.Tree.Model == nil {
		return []ui.ListItem{{ID: "none", Label: "No repository", Disabled: true}}
	}
	resolver := m.Tree.Model.GitResolver()
	if resolver == nil {
		return []ui.ListItem{{ID: "none", Label: "No repository", Disabled: true}}
	}
	statuses := resolver.AllStatuses()
	if len(statuses) == 0 {
		return []ui.ListItem{{ID: "none", Label: "Working tree clean", Disabled: true}}
	}
	type row struct {
		rel    string
		status filetree.GitStatus
	}
	rows := make([]row, 0, len(statuses))
	for rel, st := range statuses {
		if st == filetree.GitNone {
			continue
		}
		rows = append(rows, row{rel: rel, status: st})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].rel < rows[j].rel })
	if len(rows) == 0 {
		return []ui.ListItem{{ID: "none", Label: "Working tree clean", Disabled: true}}
	}
	repo := resolver.RepoPath()
	items := make([]ui.ListItem, 0, len(rows))
	for _, r := range rows {
		abs := filepath.Join(repo, r.rel)
		items = append(items, ui.ListItem{
			ID:     "git:" + abs,
			Label:  r.rel,
			Detail: filetree.StatusString(r.status),
		})
	}
	return items
}

func (m *App) handleSidebarActivate(item ui.ListItem) tea.Cmd {
	switch {
	case item.ID == sidebarItemNewTerminal:
		return m.openNewTerminal()
	case item.ID == sidebarItemNewAgent:
		return m.launcherNewSession()
	case strings.HasPrefix(item.ID, "agent:"):
		m.restoreAgent(strings.TrimPrefix(item.ID, "agent:"))
		return nil
	case strings.HasPrefix(item.ID, "term:"):
		m.focusTerminal(strings.TrimPrefix(item.ID, "term:"))
		return nil
	case strings.HasPrefix(item.ID, "git:"):
		path := strings.TrimPrefix(item.ID, "git:")
		cmd, err := m.openEditorTab(path)
		if err != nil && m.StatusBar != nil {
			m.StatusBar.SetError(fmt.Sprintf("Cannot open %s", filepath.Base(path)))
			return nil
		}
		return cmd
	}
	return nil
}

func (m *App) handleHostAction(action *ui.Action) tea.Cmd {
	if action == nil {
		return nil
	}
	if m.handleTerminalAction(*action) {
		return nil
	}
	if cmd, handled := m.handleSourceControlAction(*action); handled {
		return cmd
	}
	switch action.ID {
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
	case activitylist.ActionActivate:
		item, ok := action.Payload.(ui.ListItem)
		if ok {
			return m.handleSidebarActivate(item)
		}
	case ui.ActionSplitResize:
		payload, ok := action.Payload.(ui.SplitResize)
		if !ok {
			return nil
		}
		if payload.ID == activitylist.DividerID || payload.ID == shelltree.DividerID {
			m.setSidebarWidth(payload.First)
		}
		if payload.ID == termdock.DividerID {
			m.setTermDockHeight((m.height - 2) - payload.First)
		}
	}
	return nil
}
