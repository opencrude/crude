package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/problems"
	"crude/tui/screens/termdock"
	"crude/types"
)

// The real shell mounts the Problems pane: grouped rows paint in the dock,
// keys reach its list, a right-click opens its menu, and the pointer
// highlights rows.
func TestProblemsPaneJourney(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	openEditorTabs(t, app, "menu.go")
	root := app.ws.RootPath()
	main := filepath.Join(root, "cmd", "main.go")
	app.lspFacts = map[string]types.InsightDiagnostics{main: {
		ID: "journey-fact",
		Diagnostics: []types.LSPDiagnostic{
			{Line: 6, Col: 0, Severity: types.DiagWarning, Message: "unused value", Source: "vet"},
			{Line: 2, Col: 4, Severity: types.DiagError, Message: "undefined: helper", Source: "compiler"},
		},
	}}
	app.toggleWorkspacePanel(termdock.PanelProblems)
	paint := ansi.Strip(app.View().RenderContent())
	for _, want := range []string{"main.go", "cmd", "undefined: helper", "unused value", "✕ 1 error", "⚠ 1 warning"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("Problems pane lacks %q:\n%s", want, paint)
		}
	}
	if strings.Index(paint, "undefined: helper") > strings.Index(paint, "unused value") {
		t.Fatal("a warning is listed before an error")
	}
	if app.rootHost.Focused() != problems.ListID {
		t.Fatalf("opening Problems focused %q, want its list", app.rootHost.Focused())
	}
	_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	if text := app.problemsScreen.SelectionText(); strings.Count(text, main+":") != 2 {
		t.Fatalf("Shift+Down through the shell did not select rows: %q", text)
	}
	if _, cmd := app.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("Ctrl+C in Problems did not copy")
	}
	rect, ok := app.screenRectOf(problems.ListID)
	if !ok {
		t.Fatal("Problems list is not mounted")
	}
	_, _ = app.Update(tea.MouseMotionMsg{X: rect.X + 4, Y: rect.Y + 2})
	_, _ = app.Update(tea.MouseClickMsg{X: rect.X + 4, Y: rect.Y + 2, Button: tea.MouseRight})
	if app.LastContextMenu == nil || app.LastContextMenu.Title != "Problems" {
		t.Fatalf("right-click opened %+v", app.LastContextMenu)
	}
	labels := []string{}
	for _, section := range app.LastContextMenu.Sections {
		for _, item := range section.Items {
			labels = append(labels, item.Label)
		}
	}
	// The last row is the status next step (Trust folder in this untrusted
	// fixture, Language settings once servers run).
	if got := strings.Join(labels, ","); got != "Go to problem,Copy,Copy all,"+app.problemsModel().Status.ActionLabel {
		t.Fatalf("Problems menu = %s", got)
	}
	// Refresh restarts servers; it is not offered while they cannot run here.
	if app.problemsModel().Refresh || strings.Contains(paint, "Refresh") {
		t.Fatal("Refresh is offered in a folder where language servers cannot run")
	}
}
