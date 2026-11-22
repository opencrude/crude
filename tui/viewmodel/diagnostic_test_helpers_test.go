package viewmodel

import (
	"crude/tui/editor"
	"crude/types/insight"
)

// Source identity is copied from an actually mounted canonical projection.
// In particular these fixtures do not use the editor's PieceTable revision.
func editorDiagnosticFacts(ed *editor.Editor, diagnostics []insight.LSPDiagnostic) insight.Diagnostics {
	s := ed.DocumentSnapshot()
	version := 17
	return insight.Diagnostics{
		ID: "test-qualified-batch",
		Boundary: insight.Boundary{
			Source: insight.Source{WorkspaceGeneration: s.AnalysisGeneration, DocumentID: s.ID,
				Path: s.Path, Lifetime: s.AnalysisLifetime, Revision: s.Revision, Kind: "canonical"},
			Context:  insight.Context{ID: "test-context", Root: "/test", Generation: 1},
			Producer: insight.Producer{Provider: "test-lsp", InstanceID: "test-instance", ToolIdentity: "test-tool"},
		},
		WireVersion: version, ReportedVersion: &version, Binding: "exact-version", State: insight.Current,
		Diagnostics: diagnostics,
	}
}

func diagnosticList(service interface {
	FileDiagnostics(string) (insight.Diagnostics, bool)
}, path string) []insight.LSPDiagnostic {
	facts, _ := service.FileDiagnostics(path)
	return facts.Diagnostics
}
