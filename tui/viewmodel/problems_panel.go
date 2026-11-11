package viewmodel

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/problems"
	statusoverview "crude/tui/screens/statusoverview"
	"crude/tui/ui"
	ttypes "crude/types"
	"crude/workspace"
)

// problemRef names one diagnostic in one exact result batch. Opening checks
// it against the current batch so a stale row never moves the caret.
type problemRef struct {
	factsID string
	index   int
}

// problemsModel is the Problems pane: every reported problem with a short
// note when it cannot be opened now, and one language status with its next
// step. Grouping and order belong to the screen.
func (m *App) problemsModel() problems.Model {
	root := m.ws.RootPath()
	var list []problems.Problem
	for path, facts := range m.lspFacts {
		note := ""
		fresh := facts.State != ttypes.InsightStale && (facts.HasExactSource() || facts.FromProjectCheck())
		if tab := m.EditorState.Tabs[path]; fresh && tab != nil && tab.Editor != nil && facts.HasExactSource() && !tab.Editor.MatchesDiagnosticSource(facts) {
			fresh = false
		}
		if !fresh || !m.canNavigateDiagnostic(path, facts) {
			note = "Out of date"
		}
		folder := ""
		if rel, err := filepath.Rel(root, filepath.Dir(path)); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			folder = filepath.ToSlash(rel)
		}
		for index, diagnostic := range facts.Diagnostics {
			list = append(list, problems.Problem{
				Path: path, Folder: folder, Line: diagnostic.Line + 1, Col: diagnostic.Col + 1,
				Severity: problemSeverity(diagnostic.Severity), Message: diagnostic.Message,
				Source: diagnostic.Source, Note: note, Ref: problemRef{factsID: facts.ID, index: index},
			})
		}
	}
	insights := m.ws.LSPMgr().ProjectInsights()
	checked := insights.DiagnosticsObserved > 0 || len(list) > 0
	if insights.Language.State != "" && insights.Language.State != ttypes.LanguageReady {
		checked = false // a previous/other check cannot certify the waiting file
	}
	return problems.Model{Problems: list, Checked: checked, Status: m.problemsStatus(insights),
		Refresh: insights.Language.Action == ttypes.LanguageActionNone}
}

func problemSeverity(severity int) problems.Severity {
	switch severity {
	case workspace.DiagError:
		return problems.Error
	case workspace.DiagWarning:
		return problems.Warning
	case workspace.DiagInfo:
		return problems.Information
	}
	return problems.Hint
}

// problemsStatus says how many language servers work for this workspace. A
// state that needs the user (restricted folder, servers off) replaces the
// count with its message and names the control that fixes it.
func (m *App) problemsStatus(insights ttypes.ProjectInsights) problems.Status {
	status := problems.Status{Action: "language:settings", ActionLabel: "Language settings"}
	switch insights.Language.Action {
	case ttypes.LanguageActionTrustFolder:
		status.Action = "language:trust"
	case ttypes.LanguageActionStartServers:
		status.Action = "lsp:start-all"
	}
	if insights.Language.Action != ttypes.LanguageActionNone ||
		insights.Language.State != ttypes.LanguageReady && insights.Language.Message != "" {
		if insights.Language.Action == ttypes.LanguageActionNone {
			status.Action, status.ActionLabel = "", ""
		}
		if insights.Language.ActionLabel != "" {
			status.ActionLabel = insights.Language.ActionLabel
		}
		status.Text, status.Attention = insights.Language.Message, true
		if status.Text == "" {
			status.Text = insights.Language.Label
		}
		return status
	}
	running, starting, failed := 0, 0, 0
	for _, server := range m.ws.LSPMgr().ServerHealthSnapshots() {
		switch {
		case server.Connected:
			running++
		case server.Starting:
			starting++
		case server.Error != "":
			failed++
		}
	}
	var parts []string
	if running > 0 {
		parts = append(parts, fmt.Sprintf("%d %s running", running, plural(running, "language server")))
	}
	if starting > 0 {
		parts = append(parts, fmt.Sprintf("%d starting", starting))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d stopped with an error", failed))
		status.Attention = true
	}
	if len(parts) == 0 {
		status.Text = "No language server is running."
		return status
	}
	status.Text = strings.Join(parts, ", ")
	return status
}

func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// refreshProblems restarts the workspace's language servers through the one
// restart owner. Each new server process checks the project again and the
// stopped servers' results for unopened files go away.
func (m *App) refreshProblems() tea.Cmd {
	command := m.handleStatusOverviewNavigate(statusoverview.NavigateMsg{Action: "lsp:restart-all"})
	m.Feedback.NotifyInfo("Language servers restart. Problems update when they check the project.")
	m.syncWorkspacePanels()
	return command
}

// openProblem goes to the problem only when its result batch is still the
// current one.
func (m *App) openProblem(msg problems.OpenMsg) {
	ref, ok := msg.Problem.Ref.(problemRef)
	facts, found := m.ws.LSPMgr().FileDiagnostics(msg.Problem.Path)
	if !ok || !found || facts.ID != ref.factsID || ref.index < 0 || ref.index >= len(facts.Diagnostics) {
		m.Feedback.NotifyInfo("This problem changed. Problems now shows the current results.")
		m.syncWorkspacePanels()
		return
	}
	m.jumpToDiagnostic(msg.Problem.Path, facts.Diagnostics[ref.index])
}

// problemsMenuAt is the right-click menu of the Problems pane under the
// pointer. The row under the pointer joins the selection first, as in every
// list.
func (m *App) problemsMenuAt(mouse tea.Mouse) *ui.ContextMenuState {
	if m.problemsScreen == nil || m.rootHost == nil {
		return nil
	}
	rect, ok := m.screenRectOf(problems.ScreenID)
	if !ok || !rect.Contains(mouse.X, mouse.Y) {
		return nil
	}
	screen := m.problemsScreen
	if !screen.SelectAt(mouse.X-rect.X, mouse.Y-rect.Y) {
		return nil
	}
	_, canOpen := screen.Selected()
	if problem, ok := screen.Selected(); ok && problem.Note != "" {
		canOpen = false
	}
	return &ui.ContextMenuState{Title: "Problems", Sections: []ui.ContextMenuSection{
		{Items: []ui.ContextMenuItem{
			{Label: "Go to problem", Detail: "Enter", Disabled: !canOpen, Action: func() {
				m.runDetachedAsyncCmd(screen.OpenSelected())
			}},
			{Label: "Copy", Detail: "Ctrl+C", Action: func() { m.copyToClipboard(screen.SelectionText()) }},
			{Label: "Copy all", Action: func() { m.copyToClipboard(screen.AllText()) }},
		}},
		m.problemsStatusMenu(screen.Model()),
	}}
}

// problemsStatusMenu holds the header links: Refresh and the status action.
func (m *App) problemsStatusMenu(model problems.Model) ui.ContextMenuSection {
	var items []ui.ContextMenuItem
	if model.Refresh {
		items = append(items, ui.ContextMenuItem{Label: "Refresh", Detail: "Restart language servers", Action: func() {
			m.runDetachedAsyncCmd(m.refreshProblems())
		}})
	}
	if model.Status.ActionLabel != "" {
		items = append(items, ui.ContextMenuItem{Label: model.Status.ActionLabel, Action: func() {
			m.runDetachedAsyncCmd(m.handleStatusOverviewNavigate(statusoverview.NavigateMsg{Action: model.Status.Action}))
		}})
	}
	return ui.ContextMenuSection{Items: items}
}
