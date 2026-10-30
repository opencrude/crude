package filetree

import (
	"path/filepath"
	"testing"

	"crude/shellport"
	"crude/types"
)

// The tree owns counters only. Canonical source qualification belongs to the
// viewmodel adapter, exercised by the connected multi-shell diagnostic journey.
// This projection test uses detached facts, not a second raw manager callback.
func TestSharedDiagnosticProjectionRetainsTreePathsAndExpansion(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	mkdirForTest(t, sub)
	a, b, deep := filepath.Join(root, "a.go"), filepath.Join(root, "b.go"), filepath.Join(sub, "deep.go")
	for _, path := range []string{a, b, deep} {
		createFileForTest(t, path, "package main\n")
	}
	tree := NewFileTreeModel(root)
	v := 7
	makeFact := func(id, path string, severities ...int) types.InsightDiagnostics {
		ds := make([]types.LSPDiagnostic, 0, len(severities))
		for _, severity := range severities {
			ds = append(ds, types.LSPDiagnostic{Severity: severity})
		}
		return types.InsightDiagnostics{ID: id, Binding: "exact-version", WireVersion: 7, ReportedVersion: &v, State: types.InsightProvisional,
			Boundary: types.InsightBoundary{Source: types.InsightSource{Path: path}, Context: types.InsightContext{UnknownInputs: []string{"Build graph unknown"}}}, Diagnostics: ds}
	}
	snapshot := shellport.LSPDiagnosticSnapshot{Generation: "canonical-workspace", Version: 1, Diagnostics: map[string]types.InsightDiagnostics{
		a: makeFact("fact-a", a, types.DiagError), b: makeFact("fact-b", b, types.DiagWarning, types.DiagHint), deep: makeFact("fact-deep", deep, types.DiagError),
	}}
	var consumer shellport.LSPDiagnosticConsumer
	project := func(snapshot shellport.LSPDiagnosticSnapshot) {
		for path, fact := range consumer.Consume(snapshot) {
			errs, warns, hints := 0, 0, 0
			if fact.HasExactSource() {
				for _, d := range fact.Diagnostics {
					switch d.Severity {
					case types.DiagError:
						errs++
					case types.DiagWarning:
						warns++
					default:
						hints++
					}
				}
			}
			tree.SetDiagnostics(path, errs, warns, hints)
		}
	}
	project(snapshot)
	if node := tree.NodeByPath(a); node == nil || node.DiagErrors != 1 || node.DiagnosticCount != 1 {
		t.Fatal("observed error lost in tree")
	}
	if node := tree.NodeByPath(b); node == nil || node.DiagWarnings != 1 || node.DiagHints != 1 {
		t.Fatal("second source counters lost")
	}
	tree.Expand(sub)
	if node := tree.NodeByPath(deep); node == nil || node.DiagErrors != 1 {
		t.Fatal("pre-expansion projection lost")
	}
	if tree.Root.DiagnosticCount != 4 {
		t.Fatalf("root counter=%d", tree.Root.DiagnosticCount)
	}
	// Tree path normalization remains independent of producer/source identity.
	tree.SetDiagnostics(a+"/./.", 2, 0, 0)
	if tree.NodeByPath(a).DiagErrors != 2 {
		t.Fatal("tree counter key was not normalized")
	}
	// Stop keeps stale evidence in Problems, but a stale source is not a current
	// file-tree badge. The fact itself is never cleared or rewritten by the tree.
	stale := snapshot.Diagnostics[a]
	stale.State = types.InsightStale
	snapshot.Diagnostics[a] = stale
	snapshot.Version++
	project(snapshot)
	if tree.NodeByPath(a).DiagnosticCount != 0 || snapshot.Diagnostics[a].ID != "fact-a" || len(snapshot.Diagnostics[a].Diagnostics) != 1 {
		t.Fatal("stale projection lost provenance or painted current")
	}
}
