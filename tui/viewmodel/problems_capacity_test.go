package viewmodel

import (
	"crude/tui/screens/problems"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
	"testing"
)

// A status without a button is still a status. Shared capacity has no useful
// settings action, and results from another server do not certify a waiting file.
func TestProblemsPreservesWaitingWithoutSettingsAction(t *testing.T) {
	app, ed, service, facts := qualifiedDiagnosticApp(t)
	facts.Diagnostics = nil
	service.facts[ed.FilePath()] = facts
	const message = "Waiting for a language server. Stop an unused server."
	service.insights = types.ProjectInsights{DiagnosticsObserved: 1, Language: types.LanguageServiceStatus{
		State: types.LanguageStarting, Label: "Waiting to start", Message: message,
	}}
	app.drainLSPDiagnostics()
	model := app.problemsModel()
	if model.Checked || model.Status.Text != message || model.Status.Action != "" || model.Status.ActionLabel != "" {
		t.Fatalf("waiting is hidden or points at an ineffective setting: %+v", model)
	}
	screen := problems.New(app.Theme)
	screen.SetModel(model)
	host := ui.NewHost(app.Theme, screen)
	host.Resize(80, 8)
	host.Layout()
	buf := surface.NewCellBuffer(80, 8)
	host.Paint(buf)
	if !bufferContainsPlain(buf, "No results yet.") || bufferContainsPlain(buf, "No problems.") || !bufferContainsPlain(buf, "Waiting for a language server.") {
		t.Fatal("Problems paints a clean state or hides the capacity explanation")
	}
}
