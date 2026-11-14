package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"crude/coreprotocol"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

func TestControlLSPRestartOneAndAllUseManagerAndRetainReceipt(t *testing.T) {
	root := t.TempDir()
	w, runtime := watchManagerFixture(t, root, "")
	installWatchProducer(w.LSPMgr, root)
	connected := func(n int) bool {
		health := w.LSPMgr.ServerHealthSnapshots()
		return len(runtime.snapshot()) == n && len(health) == 1 && health[0].Connected
	}
	watchAwait(t, "initial server", func() bool { return connected(1) })
	server := w.LSPMgr.ServerHealthSnapshots()[0].ID
	closed := filepath.Join(root, "closed.fixture")
	a := &actor{ws: w}
	owner := coreprotocol.Owner{ClientID: "trusted", Epoch: 1}
	for i, target := range []string{server, "all"} {
		// Refresh rebuilds discovery. Its receipt confirms the request, not
		// a replacement process for an unused server.
		if target == "all" {
			client := runtime.snapshot()[1]
			client.muWatch.Lock()
			publish := client.publish
			client.muWatch.Unlock()
			publish(closed, types.InsightDiagnosticPublication{Diagnostics: []types.LSPDiagnostic{{Message: "old problem", Severity: types.DiagError}}})
			if _, ok := w.LSPMgr.FileDiagnostics(closed); !ok {
				t.Fatal("missing pre-refresh fact")
			}
		}
		input, _ := json.Marshal(map[string]string{"server": target})
		in := control.Request{ID: target, CommandID: "lsp.restart", Input: input}
		run, apply, handled := a.prepareControlDomain(in, owner)
		if !handled || apply != nil {
			t.Fatal("missing restart owner or unnecessary publication adapter")
		}
		result := run(t.Context())
		if result.State != control.Committed || result.Fault != nil || !strings.Contains(string(result.Output), `"restart_requested":true`) {
			t.Fatalf("restart: %+v", result)
		}
		if target == "all" {
			if _, ok := w.LSPMgr.FileDiagnostics(closed); ok {
				t.Fatal("refresh retained old closed-file problems")
			}
		}
		if i == 0 {
			watchAwait(t, "replacement server", func() bool { return connected(2) })
		}
		again := run(t.Context())
		if string(again.Output) != string(result.Output) || again.OperationID != in.ID {
			t.Fatalf("receipt changed: %+v", again)
		}
		if i == 0 && !connected(2) {
			t.Fatal("reading the same receipt restarted the server again")
		}
	}
	descriptor, ok := control.Lookup("lsp.restart")
	if !ok || descriptor.Effect != control.Execute || descriptor.Scope != control.Domain || descriptor.HumanOnly {
		t.Fatalf("wrong restart authority: %+v", descriptor)
	}
}

func TestControlLSPRestartRejectsInvalidTargetsAndCancellation(t *testing.T) {
	manager := workspace.NewLSPManager(nil)
	t.Cleanup(manager.Close)
	a := &actor{ws: &workspace.Workspace{RootPath: t.TempDir(), LSPMgr: manager}}
	owner := coreprotocol.Owner{ClientID: "trusted", Epoch: 1}
	revision := uint64(1)
	for _, in := range []control.Request{
		{Input: json.RawMessage(`{}`)},
		{Input: json.RawMessage(`{"server":"missing"}`)},
		{Input: json.RawMessage(`{"server":"all","command":"arbitrary"}`)},
		{Input: json.RawMessage(`{"server":"all"}`), Target: control.Target{ResourceID: "different"}},
		{Input: json.RawMessage(`{"server":"all"}`), Preconditions: control.Preconditions{Revision: &revision}},
	} {
		in.ID, in.CommandID = "invalid", "lsp.restart"
		run, _, handled := a.prepareControlDomain(in, owner)
		if !handled || run(t.Context()).State != control.Failed {
			t.Fatalf("invalid restart accepted: %+v", in)
		}
	}
	in := control.Request{ID: "cancelled", CommandID: "lsp.restart", Input: json.RawMessage(`{"server":"all"}`)}
	run, _, _ := a.prepareControlDomain(in, owner)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := run(ctx); got.State != control.Failed {
		t.Fatal(got)
	}
	// No trust or start policy is changed by a restart request or cancellation.
	if manager.AnalysisPolicy().Trust == types.AnalysisTrusted {
		t.Fatal("restart granted workspace trust")
	}
}
