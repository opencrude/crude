package workspace

import (
	"fmt"
	"testing"

	"crude/sessiondb"
	types "crude/types"
)

func BenchmarkProjectInsightsCached10kDiagnostics(b *testing.B) {
	manager := NewLSPManager(nil)
	diagnostics := make([]LSPDiagnostic, 100)
	for index := range diagnostics {
		diagnostics[index] = LSPDiagnostic{Line: index, Severity: 1 + index%4, Message: fmt.Sprintf("issue-%d", index)}
	}
	for file := range 100 {
		manager.setFileDiagnostics(fmt.Sprintf("file-%d.go", file), diagnostics)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = manager.ProjectInsights()
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
