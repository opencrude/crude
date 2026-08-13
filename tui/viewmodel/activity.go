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
	"crude/tui/screens/shelltree"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	activityFiles     = activitybar.ViewFiles
	activityAgents    = activitybar.ViewAgents
	activityTerminals = activitybar.ViewTerminals
	activityGit       = activitybar.ViewGit

	sidebarItemNewTerminal = "terminal.new"
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

func (m *App) toggleActivity(view activitybar.View) {
	if m.activityView == view && m.Tree != nil && m.Tree.Visible {
		m.Tree.Visible = false
		if m.FocusStack != nil && m.FocusStack.Top() == FocusTree {
			m.FocusStack.Pop()
		}
		return
	}
	m.setActivity(view, true)
	if m.FocusStack != nil {
		m.FocusStack.Clear()
		m.FocusStack.Push(FocusTree)
	}
}

func (m *App) refreshActivityList() {
	if m.activityList == nil {
		return
	}
	switch m.activityView {
	case activityAgents:
		m.activityList.SetHeading("Agents")
		m.activityList.SetItems(m.agentSidebarItems())
	case activityTerminals:
		m.activityList.SetHeading("Terminals")
		m.activityList.SetItems(m.terminalSidebarItems())
	case activityGit:
		m.activityList.SetHeading("Git")
		m.activityList.SetItems(m.gitSidebarItems())
	default:
		m.activityList.SetHeading("Files")
	}
}

func (m *App) agentSidebarItems() []ui.ListItem {
	if m.AgentMgr == nil {
		return []ui.ListItem{{ID: "none", Label: "No agents", Disabled: true}}
	}
	nodes := m.AgentMgr.AllNodes()
	if len(nodes) == 0 {
		return []ui.ListItem{{ID: "none", Label: "No agents", Disabled: true}}
	}
	items := make([]ui.ListItem, 0, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		label := workspace.AgentDisplayLabel(n)
		detail := n.Model
		if n.IsActive() {
			detail = "running"
		} else if n.IsCompleted() {
			detail = "done"
		}
		items = append(items, ui.ListItem{
			ID:     "agent:" + n.ID,
			Label:  label,
			Detail: detail,
		})
	}
	return items
}

func (m *App) terminalSidebarItems() []ui.ListItem {
	items := []ui.ListItem{{
		ID:    sidebarItemNewTerminal,
		Label: "New Terminal",
		Icon:  "+",
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
		})
	}
	return items
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
	case strings.HasPrefix(item.ID, "agent:"):
		m.switchToAgentView(strings.TrimPrefix(item.ID, "agent:"))
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
	switch action.ID {
	case activitybar.ActionSelect:
		view, ok := action.Payload.(activitybar.View)
		if ok {
			m.toggleActivity(view)
		}
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
	}
	return nil
}
