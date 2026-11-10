package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"time"

	"crude/tui/screens/problems"
	statusoverview "crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/workspace"
)

func (m *App) initWorkspacePanels() {
	if m.termDock == nil {
		return
	}
	focus := func(id ui.ComponentID) {
		if m.rootHost != nil {
			m.rootHost.Focus(id)
		}
	}
	for _, view := range []statusoverview.ViewID{statusoverview.ViewTabs, statusoverview.ViewNotifications} {
		screen := statusoverview.NewDocked(m.Theme, m.statusPanelProjection(view))
		screen.SetFocusHandler(focus)
		m.termDock.SetPanel(workspacePanelID(view), screen)
	}
	m.problemsScreen = problems.New(m.Theme)
	m.problemsScreen.SetFocusHandler(focus)
	m.problemsScreen.SetModel(m.problemsModel())
	m.termDock.SetPanel(termdock.PanelProblems, m.problemsScreen)
}

func (m *App) statusPanelProjection(view statusoverview.ViewID) statusoverview.Projection {
	projection := statusoverview.Projection{Views: make(map[statusoverview.ViewID][]statusoverview.Item), Active: view}
	switch view {
	case statusoverview.ViewNotifications:
		projection.Views[view] = m.statusNotificationItems()
	default:
		projection.Views[statusoverview.ViewTabs] = append(m.statusTabItems(), m.workspaceSessionItems()...)
		projection.HeaderAction, projection.HeaderLabel = workspaceAeronAction, "Open workspace Aeron"
	}
	return projection
}

func (m *App) syncWorkspacePanels() {
	if m.termDock == nil {
		return
	}
	version := m.ws.LSPMgr().Version()
	if m.ws.Status() != nil {
		version += m.ws.Status().Version()
	}
	second := time.Now().Unix()
	if version == m.workspacePanelLastVersion && second == m.workspacePanelLastSecond {
		return
	}
	m.workspacePanelLastVersion, m.workspacePanelLastSecond = version, second
	m.syncOpenTabStats()
	if m.panelShown(termdock.PanelWorkspace) {
		m.refreshWorkspaceSessions(false)
	}
	if m.problemsScreen != nil {
		m.problemsScreen.SetModel(m.problemsModel())
	}
	for panelID, view := range map[string]statusoverview.ViewID{
		termdock.PanelWorkspace: statusoverview.ViewTabs,
		termdock.PanelNotices:   statusoverview.ViewNotifications,
	} {
		if content := m.termDock.Panel(panelID); content != nil {
			if screen, ok := content.(*statusoverview.Surface); ok {
				screen.SetProjection(m.statusPanelProjection(view))
			}
		}
	}
}

func (m *App) toggleWorkspacePanel(panelID string) {
	if m.termDock == nil {
		return
	}
	if m.revealPlacedPanel(panelID) {
		m.preparePlacedPanel(panelID)
		return
	}
	if panelID == termdock.PanelWorkspace {
		m.refreshWorkspaceSessions(true)
	}
	m.workspacePanelLastVersion, m.workspacePanelLastSecond = 0, 0
	m.syncWorkspacePanels()
	if panelID == termdock.PanelTerminal && m.termDock.Empty() {
		if terminals := m.terminalInfos(); len(terminals) > 0 {
			m.focusTerminal(terminals[len(terminals)-1].ID)
		} else {
			m.runDetachedAsyncCmd(m.openNewTerminal())
		}
		return
	}
	if height := m.workspacePanelHeights[panelID]; height > 0 {
		m.termDockHeight = height
	} else {
		m.termDockHeight = defaultWorkspacePanelHeight(panelID)
	}
	opened := m.termDock.TogglePanel(panelID)
	m.setSessionDeckFocused(false)
	m.setTermDockFocused(opened)
	if opened {
		m.FocusStack.Clear()
		if panelID == termdock.PanelNotices && m.ws.Status() != nil {
			for _, notice := range m.ws.Status().Notifications(false) {
				if !notice.Read {
					m.ws.Status().MarkRead(notice.ID, true)
				}
			}
			m.syncWorkspacePanels()
		}
	}
}

func defaultWorkspacePanelHeight(panelID string) int {
	if panelID == termdock.PanelMemory || panelID == termdock.PanelBoard {
		return 20
	}
	if panelID == termdock.PanelPerformance {
		return 24
	}
	if panelID == termdock.PanelGit {
		return defaultGitPanelHeight
	}
	return defaultTermDockHeight
}

func (app *App) hideWorkspacePanel() {
	if app.termDock != nil {
		app.termDock.ClosePanel()
	}
	app.setTermDockFocused(false)
}

func (app *App) openTerminalPicker() {
	manager := app.termMgr()
	if manager == nil {
		return
	}
	items := []ui.ContextMenuItem{{Label: "New terminal", Action: func() { app.startSourceControlMutation(app.openNewTerminal()) }}}
	for _, terminal := range app.terminalInfos() {
		id := terminal.ID
		placement := "Docked"
		if app.terminalPlacement(terminal.ID) == workspace.TerminalFloating {
			placement = "Floating"
		} else if app.terminalPlacement(terminal.ID) == workspace.TerminalTab {
			placement = "Tab"
		}
		items = append(items, ui.ContextMenuItem{Label: terminal.DisplayName(), Detail: placement, Action: func() { app.focusTerminal(id) }})
	}
	app.openContextMenuState(tea.Mouse{X: max(0, app.width-30), Y: max(app.shellTopRow(), app.height-app.chromeBottomRows()-len(items)-3)}, &ui.ContextMenuState{Title: "Terminals", Sections: []ui.ContextMenuSection{{Items: items}}})
}

func workspacePanelID(view statusoverview.ViewID) string {
	switch view {
	case statusoverview.ViewNotifications:
		return termdock.PanelNotices
	default:
		return termdock.PanelWorkspace
	}
}
