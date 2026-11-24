package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/tui/filetree"
	"crude/tui/screens/problems"
	"crude/types/insight"
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
	service.facts = map[string]insight.Diagnostics{
		unopened: {
			ID: "project-fact", Binding: "unversioned", State: insight.Provisional, WireVersion: 1,
			Boundary: insight.Boundary{Source: insight.Source{DocumentID: "unopened:" + unopened, Path: unopened,
				Kind: insight.SourceDisk, Revision: 1, Lifetime: "producer", WorkspaceGeneration: "fixture-generation"}},
			Diagnostics: []insight.LSPDiagnostic{
				{Line: 2, Col: 4, EndLine: 2, EndCol: 9, Severity: insight.DiagError, Message: "undefined: helper"},
				{Line: 5, Col: 0, EndLine: 5, EndCol: 3, Severity: insight.DiagWarning, Message: "unused"},
			},
		},
		older: {
			ID: "older-fact", Binding: "exact-version", State: insight.Stale, WireVersion: 1,
			Boundary: insight.Boundary{Source: insight.Source{DocumentID: "older:" + older, Path: older,
				Kind: insight.SourceCanonical, Revision: 1, Lifetime: "producer", WorkspaceGeneration: "fixture-generation"}},
			Diagnostics: []insight.LSPDiagnostic{{Line: 0, Col: 0, EndLine: 0, EndCol: 1, Severity: insight.DiagError, Message: "old error"}},
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

	found := false
	for _, seg := range app.footerStatusSegments() {
		if seg.Action == "problems" {
			found = strings.Contains(seg.Text, "2✕") && strings.Contains(seg.Text, "1⚠")
		}
	}
	if !found {
		t.Fatal("footer does not count the same retained rows as the tree and Problems")
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
	// Simulate a pushed clear while the tree is hidden; no manual refresh.
	delete(service.facts, older)
	service.version++
	app.Update(TickMsg(time.Now()))
	service.hideTree = false
	app.drainLSPDiagnostics()
	errors, warnings := problemRowCounts(app.problemsModel())
	if errors != 1 || warnings != 1 || rootNode.DiagErrors != errors || app.listedErrors != errors || app.listedWarnings != warnings {
		t.Fatal("a removed problem survived in a projection")
	}

}
