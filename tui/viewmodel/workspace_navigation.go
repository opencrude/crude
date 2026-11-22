package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/core/shellport"
	"crude/tui/rendering"
	"crude/tui/screens/launcher"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	sidebarItemNewWorkspace    = "workspace.new"
	sidebarItemWorkspacePrefix = "workspace:"
)

// Both workspace home and the rail consume this one asynchronously loaded
// catalog. Opening a session may unmount home, but cannot discard navigation.
func (m *App) applyWorkspaceListing(msg launcherWorkspacesMsg) {
	m.workspaceListLoading, m.workspaceListError = false, ""
	if m.workspaceListCancel != nil {
		m.workspaceListCancel()
		m.workspaceListCancel = nil
	}
	if msg.Err != nil {
		m.workspaceListError = launcher.LoadErrorText(msg.Err)
	} else {
		m.workspaceInfos = append([]workspace.Info(nil), msg.Infos...)
	}
	if m.launcherScreen != nil && msg.Screen == m.launcherScreen {
		m.launcherScreen.FinishLoad(msg.Cwd, msg.Infos, msg.Err)
		m.launcherScreen.SetWorkspaceActivity(m.workspaceActivity.counts)
	}
	if m.activityView == activityWorkspaces {
		m.refreshActivityList()
	}
}

// workspaceRowsCache is keyed by the immutable catalog slice and presentation
// state. Activity installation invalidates it. The per-frame sidebar bind
// reuses rows instead of formatting every path, badge and tooltip again.
type workspaceRowsCache struct {
	first     *workspace.Info
	size      int
	root, err string
	loading   bool
	items     []ui.ListItem
}

func (m *App) workspaceRootMatches(path string) bool {
	current := m.ws.RootPath()
	if _, connected := m.ws.(shellport.Connection); connected {
		// Both are paths issued by the same host. The shell's case and
		// separator rules do not apply to a foreign project.
		return path != "" && current == path
	}
	return sameWorkspaceRoot(current, path)
}

func (m *App) workspaceSidebarItems() []ui.ListItem {
	state := &m.workspaceActivity
	root := m.ws.RootPath()
	var first *workspace.Info
	if len(m.workspaceInfos) != 0 {
		first = &m.workspaceInfos[0]
	}
	if rows := state.rows; rows.items != nil && rows.first == first && rows.size == len(m.workspaceInfos) &&
		rows.root == root && rows.err == m.workspaceListError && rows.loading == m.workspaceListLoading {
		return rows.items
	}
	items := make([]ui.ListItem, 1, len(m.workspaceInfos)+2)
	items[0] = ui.ListItem{ID: sidebarItemNewWorkspace, Icon: "+", Label: "New workspace", Command: true}
	for _, info := range m.workspaceInfos {
		if info.ProjectDir == "" {
			continue
		}
		current := m.workspaceRootMatches(info.ProjectDir)
		detail := ""
		if current {
			detail = "Current"
		}
		summary := state.counts[info.ID]
		items = append(items, ui.ListItem{ID: sidebarItemWorkspacePrefix + info.ProjectDir,
			Label: workspace.DisplayName(info), Detail: detail, Subtitle: info.ShortPath(), Active: current,
			SearchText: info.ProjectDir, Trailing: launcher.WorkspaceBadges(summary), Tooltip: launcher.WorkspaceTooltip(info.ProjectDir, summary)})
	}
	if m.workspaceListError != "" {
		items = append(items, ui.ListItem{ID: "workspace.error", Label: "Couldn’t refresh · " + m.workspaceListError, Disabled: true})
	} else if !m.workspaceListLoading && len(items) == 1 {
		items = append(items, ui.ListItem{ID: "workspace.empty", Label: "No recent workspaces", Disabled: true})
	}
	state.rows = workspaceRowsCache{first: first, size: len(m.workspaceInfos), root: root, err: m.workspaceListError,
		loading: m.workspaceListLoading, items: items}
	return items
}

func (m *App) workspaceSidebarPath(id string) (string, bool) {
	if m.activityView != activityWorkspaces || !m.sidebarHostVisible() {
		return "", false
	}
	for _, info := range m.workspaceInfos {
		if id == sidebarItemWorkspacePrefix+info.ProjectDir {
			return info.ProjectDir, true
		}
	}
	return "", false
}

func (m *App) workspaceNavigationActions() ui.ContextMenuSection {
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	valid := func() bool { return m.ws == owner && generation == m.workspaceSwitchGen.Load() }
	return ui.ContextMenuSection{SectionLabel: "Workspaces", Items: []ui.ContextMenuItem{
		{Label: "New workspace", Action: func() {
			if valid() {
				m.openFolderPicker()
			}
		}},
		{Label: "Refresh recent workspaces", Action: func() {
			if valid() {
				m.refreshLauncherAsync(m.ws.RootPath())
			}
		}},
	}}
}

// openWorkspacePathSwitcher is the header workspace-root dropdown: the same
// recent-workspace catalog the sidebar lists, plus its navigation context-menu
// commands. It anchors under the header like the branch picker, so
// the three identity segments behave alike.
func (m *App) openWorkspacePathSwitcher() tea.Cmd {
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	valid := func() bool { return m.ws == owner && generation == m.workspaceSwitchGen.Load() }
	items := make([]ui.ContextMenuItem, 0, len(m.workspaceInfos))
	rowIDs := make([]string, 0, len(m.workspaceInfos))
	for _, info := range m.workspaceInfos {
		path := info.ProjectDir
		if path == "" {
			continue
		}
		current := m.workspaceRootMatches(path)
		detail := rendering.TruncateMiddleVisual(info.ShortPath(), 24, "…")
		if current {
			detail = "current"
		}
		items = append(items, ui.ContextMenuItem{
			Label: workspace.DisplayName(info), Detail: detail, Disabled: current,
			Action: func() {
				if valid() {
					m.runDetachedAsyncCmd(m.launcherOpenFolder(path))
				}
			},
		})
		rowIDs = append(rowIDs, info.ID)
	}
	if len(items) == 0 {
		items = append(items, ui.ContextMenuItem{Label: "No recent workspaces", Disabled: true})
	}
	st := &ui.ContextMenuState{Title: "Workspaces", Sections: []ui.ContextMenuSection{
		{SectionLabel: "Recent", Items: items},
		m.workspaceNavigationActions(),
	}}
	cmd := m.openHeaderPopup(st)
	m.workspaceActivity.switcher, m.workspaceActivity.switcherRows = st, rowIDs
	m.refreshWorkspaceSwitcherBadges()
	return cmd
}

func (m *App) buildWorkspaceSidebarContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	menu := &ui.ContextMenuState{Title: "Recent Workspaces"}
	if item, hit := m.activityItemAtMouse(mouse); hit {
		for _, info := range m.workspaceInfos {
			if item.ID == sidebarItemWorkspacePrefix+info.ProjectDir {
				entry := launcher.WorkspaceEntry{ID: info.ID, Name: workspace.DisplayName(info), Path: info.ProjectDir, SessionCount: info.SessionCount}
				menu = m.workspaceResourceMenu(entry)
				break
			}
		}
	}
	menu.Sections = append(menu.Sections, m.workspaceNavigationActions())
	return menu
}

func (m *App) resetWorkspaceNavigation() {
	if m.workspaceListCancel != nil {
		m.workspaceListCancel()
		m.workspaceListCancel = nil
	}
	m.cancelWorkspaceActivity()
	s := &m.workspaceActivity
	s.switcher, s.switcherRows = nil, nil
	m.bindWorkspaceCatalogOwner()
	m.installWorkspaceActivity(nil)
	if m.activityView == activityWorkspaces {
		m.refreshActivityList()
	}
}

// A saved path can exist on two backends, complete with the same workspace ID.
// Preserve metadata during a same-backend switch, not across namespaces.
func (m *App) bindWorkspaceCatalogOwner() {
	s := &m.workspaceActivity
	if s.catalogOwner == m.ws {
		return
	}
	type namespaceProvider interface{ WorkspaceCatalogNamespace() string }
	prev, prevOK := s.catalogOwner.(namespaceProvider)
	next, nextOK := m.ws.(namespaceProvider)
	if s.catalogOwner != nil && (!prevOK || !nextOK || prev.WorkspaceCatalogNamespace() != next.WorkspaceCatalogNamespace()) {
		m.workspaceInfos = nil
		m.workspaceListError = ""
		m.workspaceListLoading = true
		s.rows = workspaceRowsCache{}
		if m.launcherScreen != nil {
			m.launcherScreen.SetData("", nil)
		}
	}
	s.catalogOwner = m.ws
}
