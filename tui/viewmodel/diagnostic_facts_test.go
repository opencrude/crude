package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	"crude/document"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/problems"
	"crude/types"
	"crude/workspace"
)

type diagnosticFixtureService struct {
	shellport.LSPService
	facts                map[string]types.InsightDiagnostics
	generation           string
	version              uint64
	snapshots            int
	hideEditor, hideTree bool
	insights             types.ProjectInsights
}

func (s *diagnosticFixtureService) DiagnosticCursor() (string, uint64) {
	return s.generation, s.version
}
func (s *diagnosticFixtureService) DiagnosticSnapshot() shellport.LSPDiagnosticSnapshot {
	s.snapshots++
	return shellport.LSPDiagnosticSnapshot{Generation: s.generation, Version: s.version, Diagnostics: s.AllDiagnostics()}
}
func (s *diagnosticFixtureService) AllDiagnostics() map[string]types.InsightDiagnostics {
	out := make(map[string]types.InsightDiagnostics, len(s.facts))
	for path, f := range s.facts {
		out[path] = types.CloneInsightDiagnostics(f)
	}
	return out
}
func (s *diagnosticFixtureService) FileDiagnostics(path string) (types.InsightDiagnostics, bool) {
	f, ok := s.facts[path]
	return types.CloneInsightDiagnostics(f), ok
}
func (s *diagnosticFixtureService) ShowDiagnosticsInEditor() bool                  { return !s.hideEditor }
func (s *diagnosticFixtureService) ShowDiagnosticsInTree() bool                    { return !s.hideTree }
func (s *diagnosticFixtureService) ProjectInsights() types.ProjectInsights         { return s.insights }
func (s *diagnosticFixtureService) ServerHealthSnapshots() []types.LSPServerHealth { return nil }
func (s *diagnosticFixtureService) Stop() {
	for path, f := range s.facts {
		f.State, f.Detail = types.InsightStale, "Server stopped; observation retained"
		s.facts[path] = f
	}
	s.version++
	s.insights.DiagnosticsStale = len(s.facts)
}
func (s *diagnosticFixtureService) StartForWorkspace(string) {
	s.version++
}

type diagnosticFixtureWorkspace struct {
	shellport.Workspace
	service shellport.LSPService
	root    string
}

func (w *diagnosticFixtureWorkspace) LSPMgr() shellport.LSPService { return w.service }
func (w *diagnosticFixtureWorkspace) RootPath() string             { return w.root }

func qualifiedDiagnosticApp(t *testing.T) (*App, *editor.Editor, *diagnosticFixtureService, types.InsightDiagnostics) {
	t.Helper()
	app := newTestApp(t)
	service := &diagnosticFixtureService{
		LSPService: app.ws.LSPMgr(), facts: make(map[string]types.InsightDiagnostics),
		generation: "fixture-generation", version: 1,
	}
	app.ws = &diagnosticFixtureWorkspace{Workspace: app.ws, service: service, root: t.TempDir()}
	ed := editor.New(app.Theme)
	t.Cleanup(ed.Close)
	path := filepath.Join(app.ws.RootPath(), "qualified.txt")
	if err := ed.InstallDocument(document.Snapshot{
		ID: "fixture-document", Path: path, Revision: 41, AnalysisGeneration: "fixture-generation", AnalysisLifetime: "fixture-open",
		Text: []byte("\t😀bad\n"), Saved: []byte("\t😀bad\n"), DiskExists: true,
	}); err != nil {
		t.Fatal(err)
	}
	app.EditorState.Tabs[path] = NewEditorTab(path, ed)
	app.EditorState.Editor, app.EditorState.ActiveView = ed, path
	facts := editorDiagnosticFacts(ed, []editor.LSPDiagnostic{{Line: 0, Col: 3, EndCol: 6, Severity: editor.DiagError, Message: "qualified error"}})
	service.facts[path] = facts
	return app, ed, service, facts
}

func TestQualifiedDiagnosticAllConsumersSharePublication(t *testing.T) {
	app, ed, service, facts := qualifiedDiagnosticApp(t)
	app.drainLSPDiagnostics()
	if ed.DiagnosticFacts().ID != facts.ID || ed.DocumentBuffer().Diagnostics().ID != facts.ID {
		t.Fatal("editor and buffer did not retain same fact identity")
	}
	if got := ed.Diagnostics(); len(got) != 1 || got[0].Col != 6 || service.facts[ed.FilePath()].Diagnostics[0].Col != 3 {
		t.Fatal("UTF-16 projected more than once or cache mutated")
	}
	problem, found := problemByRef(app.problemsModel(), facts.ID, 0)
	if !found {
		t.Fatal("qualified diagnostic absent from Problems")
	}
	if problem.Note != "" || problem.Severity != problems.Error || strings.Contains(problem.Message, "Fact") {
		t.Fatalf("exact observation lost qualified action: %+v", problem)
	}
}

func TestOldPublicationAfterLocalEditStaysStaleInProblems(t *testing.T) {
	app, ed, _, facts := qualifiedDiagnosticApp(t)
	app.drainLSPDiagnostics()
	ed.InsertRune('x')
	app.handleLSPDiagnosticsUpdate(workspace.LSPDiagnosticsMsg{Generation: app.workspaceSwitchGen.Load(), FilePath: ed.FilePath(), Facts: facts})
	if len(ed.DiagnosticsAt(0, 6)) != 0 || ed.FirstError() != nil {
		t.Fatal("late old publication authorized draft diagnostic interaction")
	}
	problem, found := problemByRef(app.problemsModel(), facts.ID, 0)
	if !found || problem.Note != "Out of date" || app.lspFacts[ed.FilePath()].Boundary.Source.Revision != 41 {
		t.Fatal("stale provenance was dropped or restamped")
	}
}

func TestDiagnosticDrainChecksGenerationAndVersionBeforeCopy(t *testing.T) {
	app, _, service, _ := qualifiedDiagnosticApp(t)
	app.drainLSPDiagnostics()
	for range 20 {
		app.drainLSPDiagnostics()
	}
	if service.snapshots != 1 {
		t.Fatalf("unchanged frames copied full diagnostics %d times", service.snapshots)
	}
	service.generation = "replacement-core-same-version"
	app.drainLSPDiagnostics()
	if service.snapshots != 2 {
		t.Fatal("same-version replacement generation was ignored")
	}
}

func TestLateDiagnosticEventCannotReplaceNewerAuthorityBatch(t *testing.T) {
	app, ed, service, old := qualifiedDiagnosticApp(t)
	app.drainLSPDiagnostics()
	current := types.CloneInsightDiagnostics(old)
	current.ID, current.Sequence = "newer-authoritative-publication", old.Sequence+1
	current.Diagnostics[0].Message = "newer observed error"
	service.facts[ed.FilePath()] = current
	app.handleLSPDiagnosticsUpdate(workspace.LSPDiagnosticsMsg{Generation: app.workspaceSwitchGen.Load(), FilePath: ed.FilePath(), Facts: old})
	if ed.DiagnosticFacts().ID != current.ID || app.lspFacts[ed.FilePath()].ID != current.ID {
		t.Fatal("late event replaced the canonical fact authority's newer batch")
	}
}

func TestOldWorkspaceEventCannotReprojectReplacementAuthority(t *testing.T) {
	app, ed, service, old := qualifiedDiagnosticApp(t)
	app.drainLSPDiagnostics()
	oldGeneration := app.workspaceSwitchGen.Load()
	app.workspaceSwitchGen.Add(1)
	current := types.CloneInsightDiagnostics(old)
	current.ID, current.Sequence = "replacement-workspace-publication", old.Sequence+1
	service.facts[ed.FilePath()] = current
	app.handleLSPDiagnosticsUpdate(workspace.LSPDiagnosticsMsg{
		Generation: oldGeneration, FilePath: ed.FilePath(), Facts: old,
	})
	if ed.DiagnosticFacts().ID != old.ID || app.lspFacts[ed.FilePath()].ID != old.ID {
		t.Fatal("old-workspace event was admitted against replacement authority")
	}
	app.handleLSPDiagnosticsUpdate(workspace.LSPDiagnosticsMsg{
		Generation: app.workspaceSwitchGen.Load(), FilePath: ed.FilePath(), Facts: old,
	})
	if ed.DiagnosticFacts().ID != current.ID || app.lspFacts[ed.FilePath()].ID != current.ID {
		t.Fatal("current-workspace event did not use literal replacement authority")
	}
}

func TestStopRestartRetainsStaleDiagnosticEvidence(t *testing.T) {
	app, ed, _, facts := qualifiedDiagnosticApp(t)
	app.drainLSPDiagnostics()
	app.StopLSP()
	app.startLSPForWorkspace()
	if ed.DiagnosticFacts().ID != facts.ID || app.lspFacts[ed.FilePath()].ID != facts.ID || ed.FirstError() != nil {
		t.Fatal("restart cleared evidence or certified stale source")
	}
	problem, found := problemByRef(app.problemsModel(), facts.ID, 0)
	if !found {
		t.Fatal("restart made stale problems undiscoverable")
	}
	if problem.Note == "" {
		t.Fatal("restart stale row incorrectly navigable")
	}
}

func TestDiagnosticSettingsFilterDoesNotRewriteFacts(t *testing.T) {
	app, ed, service, facts := qualifiedDiagnosticApp(t)
	service.hideEditor, service.hideTree = true, true
	app.drainLSPDiagnostics()
	if len(ed.Diagnostics()) != 0 || ed.DiagnosticFacts().ID != facts.ID || app.lspFacts[ed.FilePath()].ID != facts.ID {
		t.Fatal("filter removed shared facts or painted hidden projection")
	}
	service.hideEditor, service.hideTree = false, false
	app.drainLSPDiagnostics()
	if d := ed.FirstError(); d == nil || d.Col != 6 {
		t.Fatal("filter destroyed publication or reinterpreted visual coordinates")
	}
}

func TestUnversionedEmptyProblemsAndFooterNeverImplyClean(t *testing.T) {
	app, ed, service, facts := qualifiedDiagnosticApp(t)
	facts.Binding, facts.ReportedVersion, facts.Diagnostics = "unversioned", nil, nil
	facts.Boundary.Context = types.InsightContext{}
	service.facts[ed.FilePath()] = facts
	service.insights = types.ProjectInsights{ServerReady: true, DiagnosticsUnverified: 1}
	app.drainLSPDiagnostics()
	if model := app.problemsModel(); model.Checked || len(model.Problems) != 0 ||
		strings.Contains(strings.ToLower(model.Status.Text), "clean") || strings.Contains(model.Status.Text, "unverified") {
		t.Fatalf("unverified empty observation over-certified: %+v", model)
	}
	if summary := workspaceHealthSummary(types.ProjectInsights{ServerReady: true}); !strings.Contains(summary, "No results yet") || strings.Contains(summary, "clean") {
		t.Fatal("zero observed units implied complete coverage")
	}
}

func TestDiagnosticStatusLinkRequiresFactIdentity(t *testing.T) {
	app, ed, service, facts := qualifiedDiagnosticApp(t)
	app.drainLSPDiagnostics()
	ed.SetCursorPosition(0, 0)
	app.openProblem(problems.OpenMsg{Problem: problems.Problem{Path: ed.FilePath(), Ref: problemRef{factsID: "replaced-publication", index: 0}}})
	if ed.CursorCol() != 0 {
		t.Fatal("stale status link navigated using replacement fact")
	}
	service.facts[ed.FilePath()] = types.InsightDiagnostics{}
	app.jumpToDiagnostic(ed.FilePath(), facts.Diagnostics[0])
	if ed.CursorCol() != 0 {
		t.Fatal("raw range without observed batch authorized navigation")
	}
}

// A project check reports a file that is not open. Problems lists it, and Go
// to problem is available because opening the file reads the same disk text.
func TestProblemsListsUnopenedFileFromProjectCheck(t *testing.T) {
	app, _, service, _ := qualifiedDiagnosticApp(t)
	path := filepath.Join(app.ws.RootPath(), "unopened.txt")
	service.facts = map[string]types.InsightDiagnostics{path: {
		ID: "project-fact", Binding: "unversioned", State: types.InsightProvisional, WireVersion: 1,
		Boundary: types.InsightBoundary{Source: types.InsightSource{DocumentID: "unopened:" + path, Path: path,
			Kind: types.InsightSourceDisk, Revision: 1, Lifetime: "producer", WorkspaceGeneration: "fixture-generation"}},
		Diagnostics: []types.LSPDiagnostic{{Line: 2, Col: 4, EndLine: 2, EndCol: 9, Severity: types.DiagError, Message: "undefined: helper"}},
	}}
	service.version++
	app.drainLSPDiagnostics()
	row, ok := problemByRef(app.problemsModel(), "project-fact", 0)
	if !ok {
		t.Fatal("Problems does not list the unopened file")
	}
	if row.Note != "" || row.Line != 3 || row.Col != 5 || row.Message != "undefined: helper" {
		t.Fatalf("diagnostic row = %+v", row)
	}
}
