package core

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/document"
	"crude/types"
	"crude/workspace"
)

func TestInsightDiagnosticsTwoWireShellsAndProbeShareExactFact(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	const disk = "package main\r\n// DISK DECOY\r\n"
	const source = "package main\r\nvar _ = \"😀\" + missing\r\n"
	if err := os.WriteFile(path, []byte(disk), 0600); err != nil {
		t.Fatal(err)
	}
	engine := &semanticLSPClient{version: 40, diagnostics: []types.LSPDiagnostic{{Line: 1, Col: 15, EndLine: 1, EndCol: 22, Severity: types.DiagError, Message: "undefined: missing", Source: "fixture"}}}
	var service *workspace.LSPManager
	r, err := New(func(root string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
		service = w.LSPMgr
		service.SetRuntime(semanticLSPRuntime{engine})
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	a, b := newResourceWire(t, r, "facts-author"), newResourceWire(t, r, "facts-peer")
	var attached coreprotocol.AttachResult
	a.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
	ws := attached.WorkspaceID
	b.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
	var initial, peer document.Snapshot
	a.call(t, ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}, &initial)
	b.call(t, ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}, &peer)
	if initial.AnalysisGeneration == "" || peer.AnalysisLifetime != initial.AnalysisLifetime {
		t.Fatal("shells lost shared canonical source lifetime")
	}
	var receipt document.Receipt
	a.call(t, ws, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: initial.ID, Revision: initial.Revision,
		Edits: []document.Edit{{Start: 0, End: len(disk), Text: source}}}, &receipt)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	answer, quiet, view, err := service.ProbeDiagnostics(ctx, path)
	if err != nil || !quiet || answer.ID == "" || !answer.HasExactSource() {
		t.Fatalf("qualified probe missing: %+v quiet=%v err=%v", answer, quiet, err)
	}
	if view.SourceText != source || view.Source != answer.Boundary.Source || answer.Boundary.Source.Revision != receipt.Revision ||
		answer.WireVersion <= 40 || answer.WireVersion == int(receipt.Revision) || answer.State != types.InsightProvisional ||
		len(answer.Boundary.Context.UnknownInputs) == 0 || len(answer.Diagnostics) != 1 ||
		answer.Diagnostics[0].Message != "undefined: missing" || answer.Diagnostics[0].Col != 15 {
		t.Fatalf("probe bound wrong source/version/context: %+v %+v", answer, view)
	}
	var one, two coreprotocol.LSPSnapshot
	a.call(t, ws, coreprotocol.LSPSnapshotMethod, coreprotocol.LSPRequest{}, &one)
	b.call(t, ws, coreprotocol.LSPSnapshotMethod, coreprotocol.LSPRequest{}, &two)
	if one.Generation != initial.AnalysisGeneration || !reflect.DeepEqual(one.Diagnostics[path], answer) ||
		!reflect.DeepEqual(two.Diagnostics[path], answer) {
		t.Fatal("wire shells and probe received different fact identity/provenance")
	}
	again, _, _, err := service.ProbeDiagnostics(ctx, path)
	if err != nil || again.ID != answer.ID {
		t.Fatal("re-observation created another authoritative answer", again.ID, err)
	}
	service.Stop()
	a.call(t, ws, coreprotocol.LSPSnapshotMethod, coreprotocol.LSPRequest{}, &one)
	b.call(t, ws, coreprotocol.LSPSnapshotMethod, coreprotocol.LSPRequest{}, &two)
	stale := one.Diagnostics[path]
	if stale.ID != answer.ID || stale.State != types.InsightStale || stale.HasExactSource() ||
		len(stale.Diagnostics) != 1 || !reflect.DeepEqual(stale, two.Diagnostics[path]) || one.Insights.DiagnosticsStale != 1 {
		t.Fatalf("Stop hid or restamped warning evidence: %+v", one)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != disk {
		t.Fatal("analysis changed disk source", err)
	}
}
