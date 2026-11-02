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
	m.syncOpenTabStats()
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
	items := []ui.ContextMenuItem{{Label: "New Terminal", Action: func() { app.startSourceControlMutation(app.openNewTerminal()) }}}
	nextNumber := 1
	if app.termDock != nil {
		nextNumber += len(app.termDock.IDs())
	}
	for _, terminal := range app.terminalInfos() {
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
			{Label: "Uptime", Value: formatDuration(time.Since(m.ws.StartTime())), Role: ui.ColorMuted},
		},
	}}
	if insights.Errors == 0 && insights.Warnings == 0 && insights.Language.Message != "" {
		items[0].Lines = append([]statusoverview.DetailLine{{Value: insights.Language.Message, Role: ui.ColorFg}}, items[0].Lines...)
	}
	switch insights.Language.Action {
	case ttypes.LanguageActionTrustFolder:
		items[0].PrimaryAction = "language:trust"
	case ttypes.LanguageActionOpenSettings:
		items[0].PrimaryAction = "language:settings"
	case ttypes.LanguageActionStartServers:
		items[0].PrimaryAction = "lsp:start-all"
	case ttypes.LanguageActionRestartServer:
		// Select an exact server below. Do not restart unrelated servers.
	}
	items[0].PrimaryLabel, items[0].PrimaryEnabled = insights.Language.ActionLabel, items[0].PrimaryAction != ""
	servers := m.ws.LSPMgr().ServerHealthSnapshots()
	if insights.Language.Action == ttypes.LanguageActionRestartServer {
		match := ""
		for _, server := range servers {
			if server.Error != "" && server.Name == filepath.Base(insights.ServerName) {
				if match != "" {
					match = ""
					break
				}
				match = server.ID
			}
		}
		if match != "" {
			items[0].Owner = ttypes.ResourceRef{Kind: ttypes.ResourceLSP, ID: match}
			items[0].PrimaryAction = "lsp:restart"
			items[0].PrimaryEnabled = true
		}
	}
	if len(servers) > 0 {
		items = append(items, statusoverview.Item{ID: "workspace-lsp", Label: "Language Servers", Header: true})
	}
	for _, server := range servers {
		state := "Stopped"
		switch {
		case server.Error != "":
			state = "Language server failed"
		case server.Starting || server.Syncing:
			state = "Checking…"
		case server.Connected:
			state = "Running"
		}
		lines := []statusoverview.DetailLine{{Label: "State", Value: state, Role: mapBool(server.Error != "", ui.ColorError, ui.ColorMuted)}}
		if server.Error != "" {
			lines = append(lines, statusoverview.DetailLine{Value: "The language server failed. Check its installation and settings. Restart the server.", Role: ui.ColorError}, statusoverview.DetailLine{Label: "Details", Value: server.Error, Role: ui.ColorMuted})
		}
		actions := lspServerActions(server)
		if items[0].PrimaryAction == "lsp:restart" && items[0].Owner.ID == server.ID {
			actions = nil
		}
		items = append(items, statusoverview.Item{ID: "lsp:" + server.ID, Label: server.Name, Summary: state, Icon: "◇", Owner: ttypes.ResourceRef{Kind: ttypes.ResourceLSP, ID: server.ID}, Lines: lines, Actions: actions})
	}
	diagnostics := m.lspFacts
	if len(diagnostics) > 0 {
		items = append(items, statusoverview.Item{ID: "workspace-problems", Label: "Problems", Header: true})
	}
	paths := make([]string, 0, len(diagnostics))
	for path := range diagnostics {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		facts := diagnostics[path]
		outdated := facts.State == ttypes.InsightStale
		fromProject := facts.FromProjectCheck()
		unmatched := !facts.HasExactSource() && !fromProject
		if tab := m.EditorState.Tabs[path]; tab != nil && tab.Editor != nil && facts.HasExactSource() && !tab.Editor.MatchesDiagnosticSource(facts) {
			outdated = true
		}
		if len(facts.Diagnostics) == 0 {
			continue
		}
		file := statusoverview.Item{ID: "problem-file:" + path, Label: filepath.Base(path), Summary: fmt.Sprintf("%d problems", len(facts.Diagnostics))}
		if outdated {
			file.Lines = []statusoverview.DetailLine{{Value: "Results are for an older version of this file.", Role: ui.ColorWarning}}
		} else if unmatched {
			file.Lines = []statusoverview.DetailLine{{Value: "Results cannot be matched to this version of the file.", Role: ui.ColorWarning}}
		} else if fromProject {
			file.Lines = []statusoverview.DetailLine{{Value: "This file is not open. The project check found these problems.", Role: ui.ColorMuted}}
		}
		items = append(items, file)
		for index, diagnostic := range facts.Diagnostics {
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
				ID: fmt.Sprintf("problem:%s:%d", facts.ID, index), Label: diagnostic.Message,
				Summary: fmt.Sprintf("%d:%d  %s", diagnostic.Line+1, diagnostic.Col+1, severity), Icon: severityIconFromRole(role),
				Owner: ttypes.ResourceRef{Kind: ttypes.ResourceEditor, ID: path}, Subtitle: path,
				PrimaryAction: fmt.Sprintf("diagnostic:%s:%d:%s", facts.ID, index, path), PrimaryLabel: "Go to diagnostic", PrimaryEnabled: m.canNavigateDiagnostic(path, facts),
				Lines: []statusoverview.DetailLine{{Label: "Severity", Value: severity, Role: role}, {Label: "Location", Value: fmt.Sprintf("Ln %d, Col %d", diagnostic.Line+1, diagnostic.Col+1), Role: ui.ColorFg}, {Blank: true}, {Value: diagnostic.Message, Role: ui.ColorFg}},
			})
		}
	}
	return items
}

// lspServerActions are the lifecycle commands a server row offers. A failed
// or stopped server is retried in place (the start re-resolves the command,
// so installing a missing server and choosing Retry is enough).
func lspServerActions(server ttypes.LSPServerHealth) []ttypes.StatusAction {
	if server.Error != "" || server.Status == "Stopped" {
		return []ttypes.StatusAction{{ID: "lsp:restart", Label: "Retry", Enabled: true}}
	}
	return []ttypes.StatusAction{
		{ID: "lsp:restart", Label: "Restart", Enabled: true},
		{ID: "lsp:stop", Label: "Stop", Enabled: !server.Starting},
	}
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
