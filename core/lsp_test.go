package core

import (
	"context"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
	"encoding/json"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"reflect"
	"testing"
)

func TestLSPDiagnosticConsumersIndependentAndClears(t *testing.T) {
	var one, two shellport.LSPDiagnosticConsumer
	s := shellport.LSPDiagnosticSnapshot{Version: 1, Diagnostics: map[string][]workspace.LSPDiagnostic{"a.go": {{Message: "bad", Severity: 1}}}}
	for _, c := range []*shellport.LSPDiagnosticConsumer{&one, &two} {
		if got := c.Consume(s); got["a.go"][0].Message != "bad" {
			t.Fatal(got)
		}
	}
	s.Diagnostics["a.go"][0].Message = "caller changed"
	if got := one.Consume(shellport.LSPDiagnosticSnapshot{Version: 2}); len(got) != 1 || got["a.go"] != nil {
		t.Fatal("clear missing", got)
	}
	if got := two.Consume(shellport.LSPDiagnosticSnapshot{Version: 2}); len(got) != 1 {
		t.Fatal("another consumer drained update", got)
	}
	if got := two.Consume(shellport.LSPDiagnosticSnapshot{Version: 2}); got != nil {
		t.Fatal("unchanged version replayed")
	}
}

type lspQueryFake struct {
	shellport.LSPService
	input coreprotocol.LSPRequest
	edit  *protocol.WorkspaceEdit
}

func (f *lspQueryFake) RequestRename(_ context.Context, p string, line, col int, name string) (*protocol.WorkspaceEdit, error) {
	f.input = coreprotocol.LSPRequest{Path: p, Line: line, Col: col, NewName: name}
	return f.edit, nil
}
func TestLSPTypedRenameRoundTrip(t *testing.T) {
	edit := &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{"file:///project/a.go": {{NewText: "renamed"}}}}
	f := &lspQueryFake{edit: edit}
	in := coreprotocol.LSPRequest{Path: "/project/a.go", Line: 3, Col: 5, NewName: "renamed"}
	out, err := runLSP(context.Background(), f, coreprotocol.LSPRequestRename, in, "/project")
	if err != nil || !reflect.DeepEqual(f.input, in) {
		t.Fatal(f.input, err)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var decoded coreprotocol.LSPResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Edit == nil || decoded.Edit.Changes["file:///project/a.go"][0].NewText != "renamed" {
		t.Fatal(string(data))
	}
}
func TestLSPPrepareWorkerCaptureAndCancellation(t *testing.T) {
	manager := workspace.NewLSPManager(nil)
	t.Cleanup(manager.Close)
	a := &actor{ws: &workspace.Workspace{RootPath: "/project", LSPMgr: manager}}
	run, ok := a.prepareLSP(coreprotocol.Request{Method: coreprotocol.LSPSnapshotMethod, Payload: json.RawMessage(`{}`)})
	if !ok {
		t.Fatal("snapshot unrecognized")
	}
	a.ws.LSPMgr = nil
	result := run(context.Background())
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	var s coreprotocol.LSPSnapshot
	if err := json.Unmarshal(result.Payload, &s); err != nil || !s.Available {
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
