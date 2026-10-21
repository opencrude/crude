package viewmodel

import (
	tea "charm.land/bubbletea/v2"

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
		m.workspaceListError = msg.Err.Error()
	} else {
		m.workspaceInfos = append([]workspace.WorkspaceInfo(nil), msg.Infos...)
	}
	if m.launcherScreen != nil && msg.Screen == m.launcherScreen {
		m.launcherScreen.FinishLoad(msg.Cwd, msg.Infos, msg.Err)
	}
	if m.activityView == activityWorkspaces {
		m.refreshActivityList()
	}
}

func (m *App) workspaceSidebarItems() []ui.ListItem {
	items := []ui.ListItem{{ID: sidebarItemNewWorkspace, Icon: "+", Label: "New Workspace…", Command: true}}
	for _, info := range m.workspaceInfos {
		if info.ProjectDir == "" {
			continue
		}
		current := sameWorkspaceRoot(m.ws.RootPath(), info.ProjectDir)
		detail := ""
		if current {
			detail = "Current"
		}
		items = append(items, ui.ListItem{ID: sidebarItemWorkspacePrefix + info.ProjectDir,
			Label: workspace.WorkspaceDisplayName(info), Detail: detail, Subtitle: info.ProjectDir, Active: current})
	}
	if m.workspaceListError != "" {
		items = append(items, ui.ListItem{ID: "workspace.error", Label: "Couldn’t refresh · " + m.workspaceListError, Disabled: true})
	} else if !m.workspaceListLoading && len(items) == 1 {
		items = append(items, ui.ListItem{ID: "workspace.empty", Label: "No recent workspaces", Disabled: true})
	}
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
		{Label: "New Workspace…", Action: func() {
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
	for _, info := range m.workspaceInfos {
		path := info.ProjectDir
		if path == "" {
			continue
		}
		current := sameWorkspaceRoot(m.ws.RootPath(), path)
		detail := shortWorkspacePath(path)
		if current {
			detail = "current"
		}
		items = append(items, ui.ContextMenuItem{
			Label: workspace.WorkspaceDisplayName(info), Detail: detail, Disabled: current,
			Action: func() {
				if valid() {
					m.runDetachedAsyncCmd(m.launcherOpenFolder(path))
				}
			},
		})
	}
	if len(items) == 0 {
		items = append(items, ui.ContextMenuItem{Label: "No recent workspaces", Disabled: true})
	}
	st := &ui.ContextMenuState{Title: "Workspaces", Sections: []ui.ContextMenuSection{
		{SectionLabel: "Recent", Items: items},
		m.workspaceNavigationActions(),
	}}
	return m.openHeaderPopup(st)
}

func (m *App) buildWorkspaceSidebarContextMenu(mouse tea.Mouse) *ui.ContextMenuState {
	menu := &ui.ContextMenuState{Title: "Recent Workspaces"}
	if item, hit := m.activityItemAtMouse(mouse); hit {
		for _, info := range m.workspaceInfos {
			if item.ID == sidebarItemWorkspacePrefix+info.ProjectDir {
				entry := launcher.WorkspaceEntry{ID: info.ID, Name: workspace.WorkspaceDisplayName(info), Path: info.ProjectDir, SessionCount: info.SessionCount}
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
	if m.activityView == activityWorkspaces {
		m.refreshActivityList()
	}
}
