package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"crude/document"
	"crude/types"
)

func TestInsightDiskProbeCannotReuseRelocatedCanonicalIdentity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	createFile(t, path, "package main\n")
	docs := NewDocumentService(root, nil, nil)
	t.Cleanup(docs.Close)
	initial, err := docs.Open(document.Owner{ClientID: "editor", Epoch: 1}, path)
	if err != nil {
		t.Fatal(err)
	}
	// Literal retained relocation boundary. File-mutation correctness itself is
	// exercised by the document/file-operation journeys, not this lease test.
	docs.mu.Lock()
	docs.docs[initial.ID].snapshot.Path = filepath.Join(root, "renamed.go")
	docs.mu.Unlock()
	if lease, err := docs.acquireAnalysisSource(t.Context(), path); err == nil || lease != nil ||
		!strings.Contains(err.Error(), "identity is retained") {
		t.Fatalf("disk lease stole another canonical ID: %+v %v", lease, err)
	}
}

func TestInsightRootReplacementInvalidatesSourceContextBeforeReuse(t *testing.T) {
	m, c, initial := diagnosticFixture(t)
	c.emit(c.publication(initial.Path, []LSPDiagnostic{{Severity: types.DiagError, Message: "retained root warning"}}))
	first := requireDiagnostic(t, m, initial.Path, "retained root warning")
	m.SetWorkspaceRoot(filepath.Dir(filepath.Dir(initial.Path)))
	after, ok := m.FileDiagnostics(initial.Path)
	if !ok || after.ID != first.ID || after.State != types.InsightStale || after.HasExactSource() {
		t.Fatalf("root replacement restamped or lost source evidence: %+v", after)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if answer, _, _, err := m.ProbeDiagnostics(ctx, initial.Path); err == nil || answer.ID != "" {
		t.Fatal("cancelled root replacement probe exposed payload")
	}
}
