package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/types"
)

type diagnosticTreeProjection struct {
	id               string
	state            types.InsightState
	visible, current bool
}

func (m *App) projectTreeDiagnostic(path string, facts types.InsightDiagnostics) {
	if m.Tree == nil || m.Tree.Model == nil {
		return
	}
	projection := diagnosticTreeProjection{id: facts.ID, state: facts.State, visible: m.ws.LSPMgr() == nil || m.ws.LSPMgr().ShowDiagnosticsInTree(), current: facts.HasExactSource()}
	if tab := m.EditorState.Tabs[path]; tab != nil && tab.Editor != nil {
		projection.current = tab.Editor.MatchesDiagnosticSource(facts)
	}
	if m.lspTreeProjection == nil {
		m.lspTreeProjection = make(map[string]diagnosticTreeProjection)
	}
	if previous, ok := m.lspTreeProjection[path]; ok && previous == projection {
		return
	}
	m.lspTreeProjection[path] = projection
	errors, warnings, hints := 0, 0, 0
	if projection.visible && projection.current {
		for _, d := range facts.Diagnostics {
			switch d.Severity {
			case editor.DiagError:
				errors++
			case editor.DiagWarning:
				warnings++
			default:
				hints++
			}
		}
	}
	m.Tree.Model.SetDiagnostics(path, errors, warnings, hints)
	if m.sidebarScreen != nil {
		m.sidebarScreen.RefreshStatus()
	}
}

func (m *App) canNavigateDiagnostic(path string, facts types.InsightDiagnostics) bool {
	if !facts.HasExactSource() || facts.Boundary.Source.Path != path {
		return false
	}
	if tab := m.EditorState.Tabs[path]; tab != nil && tab.Editor != nil {
		return tab.Editor.MatchesDiagnosticSource(facts)
	}
	// A connected open validates the captured batch against the actual mounted
	// canonical source before positioning. A disk-only buffer cannot prove it.
	_, live := m.ws.(shellport.LiveDocuments)
	return live
}

func (m *App) diagnosticForNavigation(path string, d editor.LSPDiagnostic) (types.InsightDiagnostics, bool) {
	if m.ws.LSPMgr() == nil {
		return types.InsightDiagnostics{}, false
	}
	facts, ok := m.ws.LSPMgr().FileDiagnostics(path)
	if !ok {
		return facts, false
	}
	for _, observed := range facts.Diagnostics {
		if observed == d {
			return facts, true
		}
	}
	return facts, false
}

// Captures provenance along with wire coordinates. Source can overtake the
// asynchronous open; neither a matching path nor equal bytes revives this batch.
func (m *App) queueLiveDiagnosticNavigation(path string, d editor.LSPDiagnostic, facts types.InsightDiagnostics) bool {
	if _, ok := m.ws.(shellport.LiveDocuments); !ok {
		return false
	}
	cmd := m.openPaneFiles([]string{path}, m.Win.ActiveGroup())
	if cmd != nil {
		facts = types.CloneInsightDiagnostics(facts)
		m.editorCommands = append(m.editorCommands, func() tea.Msg {
			msg := cmd().(paneFilesOpenedMsg)
			msg.position, msg.diagnosticFacts = &d, &facts
			return msg
		})
	}
	return true
}
