package editor

import (
	"fmt"
	"strings"

	"crude/types"
)

// SetDiagnosticsVisible is a projection filter, not a mutation of observed facts.
func (e *Editor) SetDiagnosticsVisible(visible bool) {
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

// DiagnosticStatus describes observed scope, never whole-project cleanliness.
func (e *Editor) DiagnosticStatus() string {
	e.diagMu.RLock()
	defer e.diagMu.RUnlock()
	f := e.diagnosticFacts
	status := "Diagnostics pending"
	switch {
	case f.ID == "":
	case f.Limited || f.State == types.InsightLimited:
		status = "Diagnostics limited"
	case f.Binding != "exact-version" || f.ReportedVersion == nil:
		status = "Diagnostics unverified"
	case e.documentProjection != nil && f.HasExactSource() &&
		f.Boundary.Source.DocumentID == e.documentProjection.snapshot.ID &&
		f.Boundary.Source.Revision > e.documentProjection.snapshot.Revision:
		status = "Diagnostics pending"
	case !e.MatchesDiagnosticSource(f) || e.diagnosticsRevision != e.Revision():
		status = "Diagnostics stale"
	default:
		status = "Diagnostics observed · partial coverage"
	}
	if f.ID != "" && (f.Boundary.Context.ID == "" || f.Boundary.Context.Generation == 0 || len(f.Boundary.Context.UnknownInputs) > 0) {
		status += " · partial/context unknown"
	}
	if e.diagnosticsHidden {
		status += " · hidden"
	}
	return status
}

// DiagnosticProvenance is shared by the diagnostic tooltip and editor details.
func (e *Editor) DiagnosticProvenance() string {
	e.diagMu.RLock()
	defer e.diagMu.RUnlock()
	f := e.diagnosticFacts
	if f.ID == "" {
		return "No diagnostic publication observed"
	}
	c := f.Boundary.Context
	context := c.ID
	if context == "" {
		context = "unknown"
	}
	parts := []string{
		fmt.Sprintf("Fact %s", f.ID),
		fmt.Sprintf("%s · revision %d", f.Boundary.Producer.Provider, f.Boundary.Source.Revision),
		fmt.Sprintf("Context %s/%d", context, c.Generation),
	}
	if len(c.UnknownInputs) > 0 {
		parts = append(parts, "Unknown: "+strings.Join(c.UnknownInputs, ", "))
	}
	if f.Detail != "" {
		parts = append(parts, f.Detail)
	}
	return strings.Join(parts, " · ")
}
