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
	s := coreprotocol.LSPSnapshot{Available: true, Version: 7,
		Diagnostics: map[string][]workspace.LSPDiagnostic{"a.go": {{Message: "original"}}},
		Symbols:     map[string][]workspace.LSPSymbol{"a.go": {{Name: "parent", Children: []workspace.LSPSymbol{{Name: "child"}}}}},
		Settings:    types.LSPSettings{Languages: map[string]types.LSPLanguageSettings{"go": {Command: "gopls", Args: []string{"serve"}}}},
		Health:      []types.LSPServerHealth{{ID: "one"}}, Connected: true, Ready: true, Status: workspace.LSPConnected}
	l.install(s)
	s.Diagnostics["a.go"][0].Message = "changed"
	s.Symbols["a.go"][0].Children[0].Name = "changed"
	ds := l.FileDiagnostics("a.go")
	ds[0].Message = "caller"
	ss := l.DocumentSymbols("a.go")
	ss[0].Children[0].Name = "caller"
	if l.AllDiagnostics()["a.go"][0].Message != "original" || l.DocumentSymbols("a.go")[0].Children[0].Name != "child" {
		t.Fatal("replica aliased snapshot or reads")
	}
	st := l.Settings()
	st.Languages["go"].Args[0] = "caller"
	if l.Settings().Languages["go"].Args[0] != "serve" || !l.ServerReady() {
		t.Fatal("settings/health replica")
	}
	var consumer shellport.LSPDiagnosticConsumer
	consumer.Consume(l.DiagnosticSnapshot())
	l.install(coreprotocol.LSPSnapshot{Version: 8})
	if got := consumer.Consume(l.DiagnosticSnapshot()); len(got) != 1 || got["a.go"] != nil {
		t.Fatal("removed diagnostics not cleared", got)
	}
}

func TestLSPMutationsQueueWithoutRPC(t *testing.T) {
	w := &Workspace{}
	w.commands.init()
	l := newLSPReplica(w)
	l.OpenFile(context.Background(), "a.go", "text")
	l.CloseFile(context.Background(), "a.go")
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
