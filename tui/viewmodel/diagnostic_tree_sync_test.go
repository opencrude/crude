package viewmodel

import (
	"path/filepath"
	"testing"

	"crude/tui/filetree"
	"crude/tui/screens/problems"
	"crude/types"
)

func problemRowCounts(model problems.Model) (errors, warnings int) {
	for _, problem := range model.Problems {
		switch problem.Severity {
		case problems.Error:
			errors++
		case problems.Warning:
			warnings++
		}
	}
	return errors, warnings
}

// The file tree shows the same problems that the Problems panel lists, in
// every freshness the panel lists them: found by a project check in a file that
// is not open, and kept from an older version of the file.
func TestFileTreeMarkersMatchProblemsPanel(t *testing.T) {
	app, _, service, _ := qualifiedDiagnosticApp(t)
	root := app.ws.RootPath()
	app.Tree = NewTreeState(filetree.NewFileTreeModel(root), 30)
	unopened := filepath.Join(root, "unopened.txt")
	older := filepath.Join(root, "older.txt")
	service.facts = map[string]types.InsightDiagnostics{
		unopened: {
			ID: "project-fact", Binding: "unversioned", State: types.InsightProvisional, WireVersion: 1,
			Boundary: types.InsightBoundary{Source: types.InsightSource{DocumentID: "unopened:" + unopened, Path: unopened,
				Kind: types.InsightSourceDisk, Revision: 1, Lifetime: "producer", WorkspaceGeneration: "fixture-generation"}},
			Diagnostics: []types.LSPDiagnostic{
				{Line: 2, Col: 4, EndLine: 2, EndCol: 9, Severity: types.DiagError, Message: "undefined: helper"},
				{Line: 5, Col: 0, EndLine: 5, EndCol: 3, Severity: types.DiagWarning, Message: "unused"},
			},
		},
		older: {
			ID: "older-fact", Binding: "exact-version", State: types.InsightStale, WireVersion: 1,
			Boundary: types.InsightBoundary{Source: types.InsightSource{DocumentID: "older:" + older, Path: older,
				Kind: types.InsightSourceCanonical, Revision: 1, Lifetime: "producer", WorkspaceGeneration: "fixture-generation"}},
			Diagnostics: []types.LSPDiagnostic{{Line: 0, Col: 0, EndLine: 0, EndCol: 1, Severity: types.DiagError, Message: "old error"}},
		},
	}
	service.version++
	app.drainLSPDiagnostics()

	wantErrors, wantWarnings := problemRowCounts(app.problemsModel())
	if wantErrors != 2 || wantWarnings != 1 {
		t.Fatalf("Problems lists %d errors and %d warnings, want 2 and 1", wantErrors, wantWarnings)
	}
	rootNode := app.Tree.Model.Root
	if rootNode.DiagErrors != wantErrors || rootNode.DiagWarnings != wantWarnings {
		t.Fatalf("tree shows %d errors and %d warnings, Problems lists %d and %d",
			rootNode.DiagErrors, rootNode.DiagWarnings, wantErrors, wantWarnings)
	}

	// The tree switch hides the markers and never the list.
	service.hideTree = true
	app.drainLSPDiagnostics()
	if app.Tree.Model.Root.DiagErrors != 0 {
		t.Fatal("tree kept its markers after they were switched off")
	}
	if errors, _ := problemRowCounts(app.problemsModel()); errors != 2 {
		t.Fatal("switching the tree markers off removed problems from the panel")
	}
}
