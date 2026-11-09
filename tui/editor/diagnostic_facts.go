package editor

import "crude/types"

// SetDiagnosticsVisible is a projection filter, not a mutation of observed facts.
// It applies to every view of the document.
func (e *Editor) SetDiagnosticsVisible(visible bool) {
	e.eachView(func(v *Editor) { v.setDiagnosticsVisible(visible) })
}

func (e *Editor) setDiagnosticsVisible(visible bool) {
	e.diagMu.Lock()
	if e.diagnosticsHidden == !visible {
		e.diagMu.Unlock()
		return
	}
	e.diagnosticsHidden = !visible
	e.ClearHover()
	e.diagMu.Unlock()
}

// DiagnosticFacts is an explicit detached read; paint never copies a batch.
func (e *Editor) DiagnosticFacts() types.InsightDiagnostics {
	e.diagMu.RLock()
	defer e.diagMu.RUnlock()
	return types.CloneInsightDiagnostics(e.diagnosticFacts)
}

// DiagnosticStatus explains why error navigation has no target. It describes
// the current file, never whole-project cleanliness.
func (e *Editor) DiagnosticStatus() string {
	e.diagMu.RLock()
	defer e.diagMu.RUnlock()
	f := e.diagnosticFacts
	status := "No results yet. Open a file and start its language server."
	switch {
	case f.ID == "":
	case f.Limited || f.State == types.InsightLimited:
		status = "Results are not available for this file. Check the language server."
	case f.Binding != "exact-version" || f.ReportedVersion == nil:
		status = "Results cannot be matched to this version of the file."
	case e.documentProjection != nil && f.HasExactSource() &&
		f.Boundary.Source.DocumentID == e.documentProjection.snapshot.ID &&
		f.Boundary.Source.Revision > e.documentProjection.snapshot.Revision:
		status = "Results are not available for this version yet."
	case !e.MatchesDiagnosticSource(f) || e.diagnosticsRevision != e.Revision():
		status = "Results are for an older version of this file."
	default:
		status = "No errors to jump to in this file."
	}
	if e.diagnosticsHidden {
		status += " Problem markers are turned off."
	}
	return status
}
