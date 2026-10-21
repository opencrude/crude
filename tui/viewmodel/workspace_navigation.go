package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/launcher"
	workspacescreen "crude/tui/screens/workspaces"
	"crude/tui/ui"
	"crude/workspace"
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
	if m.workspacesScreen != nil {
		if msg.Err == nil {
			m.workspacesScreen.SetData(m.ws.RootPath(), m.workspaceInfos)
		}
		m.workspacesScreen.SetStatus(false, m.workspaceListError)
	}
}

func (m *App) ensureWorkspaceSwitcher() *workspacescreen.Screen {
	if m.workspacesScreen == nil {
		m.workspacesScreen = workspacescreen.New(m.Theme)
		m.workspacesScreen.SetFocusHandler(m.rootHost.Focus)
		m.workspacesScreen.SetData(m.ws.RootPath(), m.workspaceInfos)
		m.workspacesScreen.SetStatus(m.workspaceListLoading, m.workspaceListError)
	}
	return m.workspacesScreen
}

func (m *App) handleWorkspaceSwitcherAction(action ui.Action) (tea.Cmd, bool) {
	if !strings.HasPrefix(action.ID, "workspaces.") {
		return nil, false
	}
	request, ok := action.Payload.(workspacescreen.Request)
	if !ok || request.Screen != m.workspacesScreen || m.activityView != activityWorkspaces || !m.sidebarHostVisible() {
		return nil, true
	}
	switch action.ID {
	case workspacescreen.ActionNew:
		m.openFolderPicker()
	case workspacescreen.ActionOpen:
		if request.Screen.HasPath(request.Path) {
			return m.launcherOpenFolder(request.Path), true
		}
	}
	return nil, true
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
	screen := m.workspacesScreen
	if screen != nil && m.rootHost != nil {
		if rect, ok := m.rootHost.ScreenRect(workspacescreen.ScreenID); ok && rect.Contains(mouse.X, mouse.Y) {
			if info, hit := screen.ItemAt(mouse.X-rect.X, mouse.Y-rect.Y); hit {
				entry := launcher.WorkspaceEntry{ID: info.ID, Name: workspace.WorkspaceDisplayName(info), Path: info.ProjectDir, SessionCount: info.SessionCount}
				owner, generation := m.ws, m.workspaceSwitchGen.Load()
				valid := func() bool {
					return m.ws == owner && generation == m.workspaceSwitchGen.Load() && screen == m.workspacesScreen && screen.HasPath(entry.Path)
				}
				menu.Title = entry.Name
				menu.Sections = append(menu.Sections, ui.ContextMenuSection{Items: []ui.ContextMenuItem{
					{Label: "Open workspace", Disabled: sameWorkspaceRoot(m.ws.RootPath(), entry.Path), Action: func() {
						if valid() {
							m.runDetachedAsyncCmd(m.launcherOpenFolder(entry.Path))
						}
					}},
					{Label: "Copy path", Action: func() {
						if valid() {
							m.copyToClipboard(entry.Path)
						}
					}},
				}})
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
	// A saved catalog is application-owned metadata, not old workspace state.
	// Recreate the view so queued intents cannot bind across attachment lifetimes.
	m.workspacesScreen = nil
	if m.activityView == activityWorkspaces {
		m.ensureWorkspaceSwitcher()
	}
}
