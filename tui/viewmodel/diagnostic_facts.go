package viewmodel

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/types"
)

func diagnosticCoverageLabel(insights types.ProjectInsights) string {
	switch {
	case insights.DiagnosticsLimited > 0:
		return "limited"
	case insights.DiagnosticsStale > 0:
		return "stale"
	case insights.DiagnosticsUnverified > 0:
		return "unverified"
	case insights.ServerStarting || insights.ServerSyncing || insights.ServerBusy || insights.DiagnosticsObserved == 0:
		return "pending"
	default:
		return "partial"
	}
}

func diagnosticCoverage(insights types.ProjectInsights) string {
	text := fmt.Sprintf("%d observed source units · %d stale · %d unverified · %d limited · partial coverage",
		insights.DiagnosticsObserved, insights.DiagnosticsStale, insights.DiagnosticsUnverified, insights.DiagnosticsLimited)
	if insights.CoverageDetail != "" {
		text += " · " + insights.CoverageDetail
	}
	return text
}

func diagnosticQualification(f types.InsightDiagnostics) string {
	state := "unverified"
	switch {
	case f.Limited || f.State == types.InsightLimited:
		state = "limited"
	case f.State == types.InsightStale:
		state = "stale"
	case f.HasExactSource():
		state = "observed exact source"
	}
	if f.Boundary.Context.ID == "" || f.Boundary.Context.Generation == 0 || len(f.Boundary.Context.UnknownInputs) > 0 {
		state += " · partial/context unknown"
	} else {
		state += " · partial coverage"
	}
	return state
}

func diagnosticContext(f types.InsightDiagnostics) string {
	c := f.Boundary.Context
	id := c.ID
	if id == "" {
		id = "unknown"
	}
	text := fmt.Sprintf("%s/%d · %s", id, c.Generation, c.Root)
	if len(c.UnknownInputs) > 0 {
		text += " · unknown: " + strings.Join(c.UnknownInputs, ", ")
	}
	return text
}

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
