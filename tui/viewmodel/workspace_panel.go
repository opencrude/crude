package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"time"

	"crude/tui/screens/problems"
	"crude/tui/screens/statusoverview"
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
	notices := statusoverview.NewDocked(m.Theme, m.statusPanelProjection(statusoverview.ViewNotifications))
	notices.SetFocusHandler(focus)
	m.termDock.SetPanel(termdock.PanelNotices, notices)
	m.hub = m.newWorkspaceHub()
	m.termDock.SetPanel(termdock.PanelWorkspace, m.hub)
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
		projection.Views[statusoverview.ViewTabs] = m.statusTabItems()
	}
	return projection
}

type workspacePanelVisibility struct{ workspace, notices, problems bool }

func (m *App) syncWorkspacePanels() {
	if m.termDock == nil {
		return
	}
	visible := workspacePanelVisibility{
		workspace: m.panelShown(termdock.PanelWorkspace),
		notices:   m.panelShown(termdock.PanelNotices),
		problems:  m.panelShown(termdock.PanelProblems),
	}
	version := m.ws.LSPMgr().Version()
	if m.ws.Status() != nil {
		version += m.ws.Status().Version()
	}
	second := time.Now().Unix()
	if version == m.workspacePanelLastVersion && second == m.workspacePanelLastSecond && visible == m.workspacePanelVisible {
		return
	}
	m.workspacePanelLastVersion, m.workspacePanelLastSecond, m.workspacePanelVisible = version, second, visible
	if visible.workspace {
		m.refreshWorkspaceSessions(false)
		m.syncHubPages()
	}
	if visible.notices {
		m.syncStatusPanel(termdock.PanelNotices, statusoverview.ViewNotifications)
	}
	if visible.problems && m.problemsScreen != nil {
		m.problemsScreen.SetModel(m.problemsModel())
	}
}

func (m *App) syncStatusPanel(id string, view statusoverview.ViewID) {
	if screen, ok := m.termDock.Panel(id).(*statusoverview.Surface); ok {
		screen.SetProjection(m.statusPanelProjection(view))
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
	if panelID == termdock.PanelTerminal && m.termDock.Empty() {
		if terminals := m.terminalInfos(); len(terminals) > 0 {
			m.focusTerminal(m.adoptRetainedTerminals(terminals))
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
	if opened && panelID == termdock.PanelWorkspace {
		m.workspacePanelLastSecond = 0
		m.syncWorkspacePanels()
		m.runDetachedAsyncCmd(m.refreshWorkspaceHub(true))
	}
	if opened {
		m.FocusStack.Clear()
		if panelID == termdock.PanelNotices && m.ws.Status() != nil {
			for _, notice := range m.ws.Status().Notifications(false) {
				if !notice.Read {
					m.ws.Status().MarkRead(notice.ID, true)
				}
			}
		}
	}
	m.workspacePanelLastSecond = 0
	m.syncWorkspacePanels()
}

func defaultWorkspacePanelHeight(panelID string) int {
	if panelID == termdock.PanelMemory || panelID == termdock.PanelBoard || panelID == termdock.PanelWorkspace {
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

func (m *App) hideWorkspacePanel() {
	if m.termDock != nil {
		m.termDock.ClosePanel()
	}
	m.setTermDockFocused(false)
}

func (m *App) openTerminalPicker() {
	manager := m.termMgr()
	if manager == nil {
		return
	}
	items := []ui.ContextMenuItem{{Label: "New terminal", Action: func() { m.startSourceControlMutation(m.openNewTerminal()) }}}
	for _, terminal := range m.terminalInfos() {
		id := terminal.ID
		placement := "Docked"
		if m.terminalPlacement(terminal.ID) == workspace.TerminalFloating {
			placement = "Floating"
		} else if m.terminalPlacement(terminal.ID) == workspace.TerminalTab {
			placement = "Tab"
		}
		items = append(items, ui.ContextMenuItem{Label: terminal.DisplayName(), Detail: placement, Action: func() { m.focusTerminal(id) }})
	}
	menu := &ui.ContextMenuState{Title: "Terminals", Sections: []ui.ContextMenuSection{{Items: items}}}
	// The menu opens at its button in the panel header, or at the footer's
	// Terminals item when the panel is hidden.
	if anchor, ok := m.screenRectOf(ui.NewID(termdock.HeaderID, "terminal.picker")); ok && !anchor.Empty() {
		m.openMenuFor(anchor, menu)
		return
	}
	if anchor, ok := m.footerSegmentRect("terminals"); ok {
		m.openMenuFor(anchor, menu)
		return
	}
	m.openContextMenuState(tea.Mouse{X: max(0, m.width-30), Y: max(m.shellTopRow(), m.height-m.chromeBottomRows()-len(items)-3)}, menu)
}
