package workspace

import (
	"fmt"
	"testing"

	"crude/sessiondb"
	types "crude/types"
)

func BenchmarkProjectInsightsCached10kDiagnostics(b *testing.B) {
	manager := diagnosticSummaryFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = manager.ProjectInsights()
	}
}

func diagnosticSummaryFixture(b testing.TB) *LSPManager {
	b.Helper()
	manager := NewLSPManager(nil)
	docs := NewDocumentService(b.TempDir(), nil, manager)
	b.Cleanup(manager.Close)
	b.Cleanup(docs.Close)
	diagnostics := make([]LSPDiagnostic, 100)
	for index := range diagnostics {
		diagnostics[index] = LSPDiagnostic{Line: index, EndLine: index, Severity: 1 + index%4, Message: fmt.Sprintf("issue-%d", index)}
	}
	for file := range 100 {
		path := fmt.Sprintf("file-%d.go", file)
		boundary := types.InsightBoundary{
			Source:   types.InsightSource{WorkspaceGeneration: docs.Insight.generation, DocumentID: path, Path: path, Lifetime: "fixture", Revision: 1, Kind: "canonical"},
			Context:  types.InsightContext{ID: "fixture", Root: "project", Generation: 1, UnknownInputs: []string{"Incomplete build graph"}},
			Producer: types.InsightProducer{InstanceID: "fixture", Provider: "fixture", ToolIdentity: "fixture"},
		}
		if err := docs.Insight.diagnostics.Observe(boundary); err != nil {
			b.Fatal(err)
		}
		v := 7
		if _, err := docs.Insight.diagnostics.Publish(types.InsightDiagnosticPublication{Boundary: boundary, Sequence: 1, WireVersion: 7, ReportedVersion: &v, Diagnostics: diagnostics}); err != nil {
			b.Fatal(err)
		}
	}
	view := manager.ProjectInsights()
	if view.Errors != 2500 || view.Warnings != 2500 || view.DiagnosticsObserved != 100 || view.DiagnosticsUnverified != 100 {
		b.Fatalf("qualified dense fixture not observed: %+v", view)
	}
	return manager
}

func TestProjectInsightsUnchangedReadDoesNotExportDenseFacts(t *testing.T) {
	manager := diagnosticSummaryFixture(t)
	if allocations := testing.AllocsPerRun(20, func() { _ = manager.ProjectInsights() }); allocations != 0 {
		t.Fatalf("unchanged status read allocated %.0f times", allocations)
	}
}

func BenchmarkUsageSummaryCached1000Requests(b *testing.B) {
	session := NewSession("session", "root", nil, nil)
	for index := range 1000 {
		id := fmt.Sprintf("request-%d", index)
		session.usageRecords[id] = sessiondb.UsageRecord{
			RequestID: id, SessionID: "session", AgentID: "root", PromptTokens: 1000,
			CompletionTokens: 100, CostUSD: 0.001, CostKnown: true,
			ModelID: "model", CreatedAt: int64(index + 1), UpdatedAt: int64(index + 1),
		}
	}
	workspace := &Workspace{AgentMgr: NewAgentManager(), SessionsByAgent: map[string]*Session{"root": session}}
	workspace.AgentMgr.AddOrchestrator("root", NewAgentState("root", nil, nil))
	if _, ok := workspace.UsageSummary("root", types.UsageThisAgent); !ok {
		b.Fatal("usage fixture did not summarize")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = workspace.UsageSummary("root", types.UsageThisAgent)
	}
}
