package remote

import (
	"context"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
	"testing"
)

func TestLSPReplicaReadIsolationAndClear(t *testing.T) {
	l := newLSPReplica(nil) // Any RPC during a read would panic.
	version := 9
	s := coreprotocol.LSPSnapshot{Available: true, Generation: "owner-1", Version: 7, ContextGeneration: 3,
		Diagnostics: map[string]types.InsightDiagnostics{"a.go": {ID: "shared-fact", ReportedVersion: &version, Boundary: types.InsightBoundary{Context: types.InsightContext{UnknownInputs: []string{"unknown build"}}}, Diagnostics: []types.LSPDiagnostic{{Message: "original"}}}},
		Symbols:     map[string][]workspace.LSPSymbol{"a.go": {{Name: "parent", Children: []workspace.LSPSymbol{{Name: "child"}}}}},
		Settings:    types.LSPSettings{Languages: map[string]types.LSPLanguageSettings{"go": {Command: "gopls", Args: []string{"serve"}}}},
		Health:      []types.LSPServerHealth{{ID: "one"}}, Connected: true, Ready: true, Status: workspace.LSPConnected}
	l.install(s)
	if owner, context := l.ContextCursor(); owner != "owner-1" || context != 3 {
		t.Fatal("replica lost the independent semantic-context cursor", owner, context)
	}
	s.Diagnostics["a.go"].Diagnostics[0].Message = "changed"
	*s.Diagnostics["a.go"].ReportedVersion = 99
	s.Diagnostics["a.go"].Boundary.Context.UnknownInputs[0] = "changed"
	s.Symbols["a.go"][0].Children[0].Name = "changed"
	ds, ok := l.FileDiagnostics("a.go")
	if !ok || ds.ID != "shared-fact" || *ds.ReportedVersion != 9 || ds.Boundary.Context.UnknownInputs[0] != "unknown build" {
		t.Fatalf("fact provenance changed: %+v", ds)
	}
	ds.Diagnostics[0].Message = "caller"
	*ds.ReportedVersion = 100
	ds.Boundary.Context.UnknownInputs[0] = "caller"
	ss := l.DocumentSymbols("a.go")
	ss[0].Children[0].Name = "caller"
	if l.AllDiagnostics()["a.go"].Diagnostics[0].Message != "original" || l.DocumentSymbols("a.go")[0].Children[0].Name != "child" {
		t.Fatal("replica aliased snapshot or reads")
	}
	st := l.Settings()
	st.Languages["go"].Args[0] = "caller"
	if l.Settings().Languages["go"].Args[0] != "serve" || !l.ServerReady() {
		t.Fatal("settings/health replica")
	}
	var consumer shellport.LSPDiagnosticConsumer
	consumer.Consume(l.DiagnosticSnapshot())
	if generation, version := l.DiagnosticCursor(); consumer.NeedsSnapshot(generation, version) {
		t.Fatal("unchanged snapshot requested")
	}
	l.install(coreprotocol.LSPSnapshot{Generation: "owner-2", Version: 7})
	if generation, version := l.DiagnosticCursor(); !consumer.NeedsSnapshot(generation, version) {
		t.Fatal("new owner reused old version")
	}
	if got := consumer.Consume(l.DiagnosticSnapshot()); len(got) != 1 || got["a.go"].ID != "" {
		t.Fatal("removed diagnostics not cleared", got)
	}
}

func TestLSPMutationsQueueWithoutRPC(t *testing.T) {
	w := &Workspace{}
	w.commands.init()
	l := newLSPReplica(w)
	l.NotifyWatchedFilesChanged(context.Background(), "a.go", 2)
	l.SetActiveFile("a.go")
	l.Stop()
	for i := 0; i < 3; i++ {
		if fn, ok := w.commands.pop(); !ok || fn == nil {
			t.Fatal("mutation not queued", i)
		}
	}
	if _, ok := w.commands.pop(); ok {
		t.Fatal("extra commands (implicit startup/stop)")
	}
}
