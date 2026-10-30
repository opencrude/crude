package viewmodel

import (
	"crude/tui/editor"
	"crude/types"
)

// Source identity is copied from an actually mounted canonical projection.
// In particular these fixtures do not use the editor's PieceTable revision.
func editorDiagnosticFacts(ed *editor.Editor, diagnostics []editor.LSPDiagnostic) types.InsightDiagnostics {
	s := ed.DocumentSnapshot()
	version := 17
	return types.InsightDiagnostics{
		ID: "test-qualified-batch",
		Boundary: types.InsightBoundary{
			Source: types.InsightSource{WorkspaceGeneration: s.AnalysisGeneration, DocumentID: s.ID,
				Path: s.Path, Lifetime: s.AnalysisLifetime, Revision: s.Revision, Kind: "canonical"},
			Context:  types.InsightContext{ID: "test-context", Root: "/test", Generation: 1},
			Producer: types.InsightProducer{Provider: "test-lsp", InstanceID: "test-instance", ToolIdentity: "test-tool"},
		},
		WireVersion: version, ReportedVersion: &version, Binding: "exact-version", State: types.InsightCurrent,
		Diagnostics: diagnostics,
	}
}

func diagnosticList(service interface {
	FileDiagnostics(string) (types.InsightDiagnostics, bool)
}, path string) []types.LSPDiagnostic {
	facts, _ := service.FileDiagnostics(path)
	return facts.Diagnostics
}
