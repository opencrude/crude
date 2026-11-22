package editor

import (
	"fmt"
	"sync/atomic"

	"crude/types/insight"
	"crude/workspace/document"
)

var diagnosticTestSequence atomic.Uint64

// Test fixtures use a real canonical document projection with a clock deliberately
// distinct from the PieceTable counter. Production has no raw diagnostic adapter.
func setTestDiagnostics(e *Editor, diagnostics []insight.LSPDiagnostic) {
	if e.documentProjection == nil {
		view := e.captureViewState()
		s := document.Snapshot{
			ID: "diagnostic-document", Path: e.filePath, Revision: 41,
			AnalysisGeneration: "diagnostic-workspace", AnalysisLifetime: "diagnostic-open",
			Text: []byte(e.Value()), Saved: []byte(e.Value()), DiskExists: true,
		}
		if err := e.InstallDocument(s); err != nil {
			panic(err)
		}
		e.restoreViewState(view)
	}
	e.SetDiagnostics(testDiagnosticFacts(e, diagnostics))
}

func testDiagnosticFacts(e *Editor, diagnostics []insight.LSPDiagnostic) insight.Diagnostics {
	s := e.DocumentSnapshot()
	v := 7
	return insight.Diagnostics{
		ID: fmt.Sprintf("diagnostic-fact-%d", diagnosticTestSequence.Add(1)),
		Boundary: insight.Boundary{
			Source: insight.Source{WorkspaceGeneration: s.AnalysisGeneration, DocumentID: s.ID, Path: s.Path,
				Lifetime: s.AnalysisLifetime, Revision: s.Revision, Kind: "canonical"},
			Context:  insight.Context{ID: "build-context", Root: "/fixture", Generation: 1},
			Producer: insight.Producer{InstanceID: "test-server", Provider: "test-lsp", ToolIdentity: "test-v1"},
		},
		WireVersion: v, ReportedVersion: &v, Binding: "exact-version", State: insight.Current,
		Diagnostics: diagnostics,
	}
}
