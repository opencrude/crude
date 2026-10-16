package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	statusoverview "crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	ttypes "crude/types"
	"crude/workspace"
)

func (m *App) initWorkspacePanels() {
	if m.termDock == nil {
		return
	}
	for _, view := range []statusoverview.ViewID{statusoverview.ViewTabs, statusoverview.ViewWorkspace, statusoverview.ViewNotifications} {
		screen := statusoverview.NewDocked(m.Theme, m.statusPanelProjection(view))
		screen.SetFocusHandler(func(id ui.ComponentID) {
			if m.rootHost != nil {
				m.rootHost.Focus(id)
			}
		})
		m.termDock.SetPanel(workspacePanelID(view), screen)
	}
}

func (m *App) statusPanelProjection(view statusoverview.ViewID) statusoverview.Projection {
	projection := statusoverview.Projection{Views: make(map[statusoverview.ViewID][]statusoverview.Item), Active: view}
	switch view {
	case statusoverview.ViewWorkspace:
		projection.Views[view] = m.problemPanelItems()
	case statusoverview.ViewNotifications:
		projection.Views[view] = m.statusNotificationItems()
	default:
		projection.Views[statusoverview.ViewTabs] = append(m.statusTabItems(), m.workspaceSessionItems()...)
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
	if m.panelShown(termdock.PanelWorkspace) {
		m.refreshWorkspaceSessions(false)
	}
	for panelID, view := range map[string]statusoverview.ViewID{
		termdock.PanelWorkspace: statusoverview.ViewTabs,
		termdock.PanelProblems:  statusoverview.ViewWorkspace,
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
		m.runDetachedAsyncCmd(m.openNewTerminal())
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

// activeDockPanel is the surface the dock shows, for the move commands.
func (app *App) activeDockPanel() string {
	if app.termDock == nil || !app.termDock.IsOpen() {
		return ""
	}
	return app.termDock.ActivePanel()
}

func (app *App) openTerminalPicker() {
	manager := app.termMgr()
	if manager == nil {
		return
	}
	items := []ui.ContextMenuItem{{Label: "New Terminal", Action: func() { app.startSourceControlMutation(app.openNewTerminal()) }}}
	nextNumber := 1
	if app.termDock != nil {
		nextNumber += len(app.termDock.IDs())
	}
	for _, terminal := range manager.List() {
		id := terminal.ID
		title := terminal.Title
		if title == "" {
			title = "Terminal"
		}
		number := 0
		if app.termDock != nil {
			number = app.termDock.Number(id)
		}
		if number == 0 {
			number = nextNumber
			nextNumber++
		}
		placement := "Docked"
		if app.terminalPlacement(terminal.ID) == workspace.TerminalFloating {
			placement = "Floating"
		}
		items = append(items, ui.ContextMenuItem{Label: fmt.Sprintf("%s %d", title, number), Detail: placement, Action: func() { app.focusTerminal(id) }})
	}
	app.openContextMenuState(tea.Mouse{X: max(0, app.width-30), Y: max(app.shellTopRow(), app.height-app.chromeBottomRows()-len(items)-3)}, &ui.ContextMenuState{Title: "Terminals", Sections: []ui.ContextMenuSection{{Items: items}}})
}

func (m *App) problemPanelItems() []statusoverview.Item {
	insights := m.ws.LSPMgr().ProjectInsights()
	items := []statusoverview.Item{{
		ID: "workspace:summary", Label: "Workspace health", Summary: workspaceHealthSummary(insights), Active: true,
		Owner: ttypes.ResourceRef{Kind: ttypes.ResourceWorkspace, ID: m.ws.RootPath()},
		Lines: []statusoverview.DetailLine{
			{Label: "Path", Value: m.ws.RootPath(), Role: ui.ColorFg},
			{Label: "Errors", Value: fmt.Sprintf("%d", insights.Errors), Role: mapBool(insights.Errors > 0, ui.ColorError, ui.ColorMuted)},
			{Label: "Warnings", Value: fmt.Sprintf("%d", insights.Warnings), Role: mapBool(insights.Warnings > 0, ui.ColorWarning, ui.ColorMuted)},
			{Label: "Affected files", Value: fmt.Sprintf("%d", insights.FilesAffected), Role: ui.ColorMuted},
			{Label: "Tracked files", Value: fmt.Sprintf("%d", insights.FilesTracked), Role: ui.ColorMuted},
			{Label: "Uptime", Value: formatDuration(time.Since(m.ws.StartTime())), Role: ui.ColorMuted},
		},
	}}
	servers := m.ws.LSPMgr().ServerHealthSnapshots()
	if len(servers) > 0 {
		items = append(items, statusoverview.Item{ID: "workspace-lsp", Label: "Language Servers", Header: true})
	}
	for _, server := range servers {
		lines := []statusoverview.DetailLine{
			{Label: "State", Value: server.Status, Role: mapBool(server.Connected, ui.ColorSuccess, mapBool(server.Error != "", ui.ColorError, ui.ColorWarning))},
			{Label: "Root", Value: server.RootURI, Role: ui.ColorFg},
		}
		if server.Busy || server.Syncing {
			lines = append(lines, statusoverview.DetailLine{Label: "Activity", Value: mapBool(server.Busy, "Busy", "Syncing"), Role: ui.ColorAccent})
		}
		if server.Error != "" {
			lines = append(lines, statusoverview.DetailLine{Label: "Error", Value: server.Error, Role: ui.ColorError})
		}
		items = append(items, statusoverview.Item{ID: "lsp:" + server.ID, Label: server.Name, Summary: server.Status, Icon: "◇", Owner: ttypes.ResourceRef{Kind: ttypes.ResourceLSP, ID: server.ID}, Lines: lines})
	}

	diagnostics := m.ws.LSPMgr().AllDiagnostics()
	if len(diagnostics) > 0 {
		items = append(items, statusoverview.Item{ID: "workspace-problems", Label: "Problems", Header: true})
	}
	paths := make([]string, 0, len(diagnostics))
	for path := range diagnostics {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		items = append(items, statusoverview.Item{ID: "problem-file:" + path, Label: filepath.Base(path), Summary: fmt.Sprintf("%d problems", len(diagnostics[path])), Header: true})
		for index, diagnostic := range diagnostics[path] {
			severity, role := "Hint", ui.ColorMuted
			switch diagnostic.Severity {
			case workspace.DiagError:
				severity, role = "Error", ui.ColorError
			case workspace.DiagWarning:
				severity, role = "Warning", ui.ColorWarning
			case workspace.DiagInfo:
				severity = "Information"
			}
			items = append(items, statusoverview.Item{
				ID: fmt.Sprintf("problem:%s:%d:%d:%d", path, diagnostic.Line, diagnostic.Col, index), Label: diagnostic.Message,
				Summary: fmt.Sprintf("%d:%d  %s", diagnostic.Line+1, diagnostic.Col+1, severity), Icon: severityIconFromRole(role),
				Owner: ttypes.ResourceRef{Kind: ttypes.ResourceEditor, ID: path}, Subtitle: path,
				Lines: []statusoverview.DetailLine{{Label: "Severity", Value: severity, Role: role}, {Label: "Location", Value: fmt.Sprintf("Ln %d, Col %d", diagnostic.Line+1, diagnostic.Col+1), Role: ui.ColorFg}, {Blank: true}, {Value: diagnostic.Message, Role: ui.ColorFg}},
			})
		}
	}
	for _, issue := range m.ws.Status().Issues() {
		if issue.ResolvedAt.IsZero() && issue.Owner.Kind == ttypes.ResourceLSP {
			items = append(items, statusoverview.Item{ID: "issue:" + issue.ID, Label: issue.Summary, Summary: issue.Detail, Icon: "!", Owner: issue.Owner, Lines: []statusoverview.DetailLine{{Label: "Issue", Value: issue.Detail, Role: severityRole(issue.Severity)}}})
		}
	}
	return items
}

func severityIconFromRole(role ui.SemanticColor) string {
	switch role {
	case ui.ColorError:
		return "✕"
	case ui.ColorWarning:
		return "⚠"
	default:
		return "i"
	}
}

func workspacePanelID(view statusoverview.ViewID) string {
	switch view {
	case statusoverview.ViewWorkspace:
		return termdock.PanelProblems
	case statusoverview.ViewNotifications:
		return termdock.PanelNotices
	default:
		return termdock.PanelWorkspace
	}
}
