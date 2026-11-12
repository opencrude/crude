package core

import (
	"context"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
	"reflect"
	"testing"
)

func TestLSPDiagnosticConsumersIndependentAndClears(t *testing.T) {
	var one, two shellport.LSPDiagnosticConsumer
	s := shellport.LSPDiagnosticSnapshot{Generation: "workspace-1", Version: 1, Diagnostics: map[string]types.InsightDiagnostics{"a.go": {ID: "fact-1", Diagnostics: []types.LSPDiagnostic{{Message: "bad", Severity: 1}}}}}
	for _, c := range []*shellport.LSPDiagnosticConsumer{&one, &two} {
		if got := c.Consume(s); got["a.go"].Diagnostics[0].Message != "bad" || got["a.go"].ID != "fact-1" {
			t.Fatal(got)
		}
	}
	s.Diagnostics["a.go"].Diagnostics[0].Message = "caller changed"
	if got := one.Consume(shellport.LSPDiagnosticSnapshot{Version: 2}); len(got) != 1 || got["a.go"].ID != "" {
		t.Fatal("clear missing", got)
	}
	if got := two.Consume(shellport.LSPDiagnosticSnapshot{Version: 2}); len(got) != 1 {
		t.Fatal("another consumer drained update", got)
	}
	if got := two.Consume(shellport.LSPDiagnosticSnapshot{Version: 2}); got != nil {
		t.Fatal("unchanged version replayed")
	}
}

func TestLSPTypedRenameRequiresCoreReviewInsteadOfRawShellEdits(t *testing.T) {
	a := &actor{ws: &workspace.Workspace{}}
	if _, ok := a.prepareLSP(coreprotocol.Request{Method: "lsp.rename"}); ok {
		t.Fatal("retired raw edit route is still admitted")
	}
	in := coreprotocol.RenamePreviewRequest{Source: types.InsightSource{DocumentID: "doc", Revision: 7, Kind: "canonical", Path: "/project/a.go"}, Line: 3, Col: 5, NewName: "renamed"}
	raw := mustTestPayload(in)
	var decoded coreprotocol.RenamePreviewRequest
	if err := coreprotocol.Decode(raw, &decoded); err != nil || !reflect.DeepEqual(decoded, in) {
		t.Fatal(decoded, err)
	}
	run, ok := a.prepareLSP(coreprotocol.Request{Method: coreprotocol.LSPPrepareRename, Payload: raw})
	if !ok {
		t.Fatal("reviewed rename route missing")
	}
	out := run(context.Background())
	if out.Error == nil || out.Error.Code != "review_failed" {
		t.Fatal("missing canonical/history authority was accepted", out)
	}
}
func TestLSPPrepareWorkerCaptureAndCancellation(t *testing.T) {
	manager := workspace.NewLSPManager(nil)
	t.Cleanup(manager.Close)
	a := &actor{ws: &workspace.Workspace{RootPath: "/project", LSPMgr: manager}}
	run, ok := a.prepareLSP(coreprotocol.Request{Method: coreprotocol.LSPSnapshotMethod, Payload: mustTestPayload(coreprotocol.LSPRequest{})})
	if !ok {
		t.Fatal("snapshot unrecognized")
	}
	a.ws.LSPMgr = nil
	result := run(context.Background())
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	var s coreprotocol.LSPSnapshot
	if err := coreprotocol.Decode(result.Payload, &s); err != nil || !s.Available {
		t.Fatal(s, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := run(ctx); r.Error == nil || r.Error.Code != "cancelled" {
		t.Fatal(r)
	}
	if _, ok := a.prepareLSP(coreprotocol.Request{Method: "lsp.arbitrary"}); ok {
		t.Fatal("open-ended dispatch")
	}
}
func TestLSPSnapshotSettingsDetached(t *testing.T) {
	manager := workspace.NewLSPManager(nil)
	t.Cleanup(manager.Close)
	manager.SetSettings(types.LSPSettings{Languages: map[string]types.LSPLanguageSettings{"go": {Command: "test", Args: []string{"serve"}}}})
	s := lspSnapshot(manager)
	s.Settings.Languages["go"].Args[0] = "changed"
	if manager.Settings().Languages["go"].Args[0] != "serve" {
		t.Fatal("snapshot shared settings")
	}
}

func TestPassiveLSPReadsDoNotUseFileEffectLane(t *testing.T) {
	for _, method := range []string{coreprotocol.LSPRequestHover, coreprotocol.LSPRequestSignatureHelp, coreprotocol.LSPNavigationMethod, coreprotocol.LSPCompletionsMethod, coreprotocol.LSPInlayHintsMethod, coreprotocol.LSPCodeActions, coreprotocol.LSPSelectionFormattingMethod} {
		if !independentLSPRead(method) {
			t.Fatal("read blocks file effects", method)
		}
	}
	for _, method := range []string{coreprotocol.DocumentEdit, coreprotocol.LSPStop, coreprotocol.LSPRefresh, coreprotocol.LSPNotifyWatchedFilesChanged, coreprotocol.ReviewedEditApply, coreprotocol.LSPPrepareCodeAction, coreprotocol.LSPPrepareRename, "lsp.unknown"} {
		if independentLSPRead(method) {
			t.Fatal("effect escaped ordered owner", method)
		}
	}
}
