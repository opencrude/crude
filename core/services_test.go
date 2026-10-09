package core

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/memory"
	"crude/types"
	"crude/workspace"

	gogit "github.com/go-git/go-git/v5"
)

// Exercise the service contract over the real wire/client without relying on the
// separately owned actor worker integration.
func TestGitServiceFacadeWireAndReviewedMutation(t *testing.T) {
	root := t.TempDir()
	if _, err := gogit.PlainInit(root, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte("reviewed bytes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	ws.Git = workspace.NewGitService(root)
	actor := &actor{ws: ws}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	left, right := net.Pipe()
	done := make(chan error, 1)
	go func() {
		f := corewire.NewFramer(ctx, right)
		defer f.Close()
		id, _, err := f.NegotiateServer(corewire.MaxFrameSize)
		if err != nil {
			done <- err
			return
		}
		owner := coreprotocol.Owner{ClientID: id, Epoch: 1}
		raw, _ := json.Marshal(owner)
		if err := f.Write(corewire.Frame{Version: 1, Kind: corewire.KindEvent, ClientID: id, Epoch: 1, Method: "connection.ready", Payload: raw}); err != nil {
			done <- err
			return
		}
		for count := 0; count < 5; count++ {
			frame, err := f.Read()
			if err != nil {
				done <- err
				return
			}
			request := coreprotocol.Request{ID: frame.RequestID, Owner: owner, WorkspaceID: frame.WorkspaceID, Method: frame.Method, Payload: frame.Payload}
			run, ok := actor.prepareService(request)
			if !ok {
				done <- context.Canceled
				return
			}
			result := run(ctx)
			out := corewire.Frame{Version: 1, Kind: corewire.KindResponse, RequestID: request.ID, ClientID: id, Epoch: 1, WorkspaceID: request.WorkspaceID, Method: request.Method, Payload: result.Payload}
			if result.Error != nil {
				out.Error = &corewire.Fault{Code: result.Error.Code, Message: result.Error.Message}
			}
			if err := f.Write(out); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	client, err := coreclient.Dial(ctx, left, "service-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	service := coreclient.NewGitService(client, "exact-workspace")
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Changes) != 1 || snapshot.Changes[0].Path != "sample.txt" {
		t.Fatalf("snapshot: %+v", snapshot)
	}
	comparison, err := service.Compare(ctx, snapshot.Changes[0])
	if err != nil {
		t.Fatal(err)
	}
	if comparison.After != "reviewed bytes\n" {
		t.Fatalf("wrong content: %+v", comparison)
	}
	if err := service.StageReviewed(ctx, comparison); err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.Snapshot(ctx)
	if err != nil || len(snapshot.StagedPaths()) != 1 {
		t.Fatalf("not staged: %+v %v", snapshot, err)
	}
	if err := service.StageReviewed(ctx, comparison); err == nil {
		t.Fatal("stale review unexpectedly accepted")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServiceDispatcherClosedAndValidation(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	a := &actor{ws: ws}
	// Retain the constructor-owned persistence for cleanup, but explicitly
	// exercise unavailable service captures rather than depending on defaults.
	db, board := ws.WorkspaceDB, ws.Collaboration
	ws.WorkspaceDB, ws.Collaboration = nil, nil
	defer func() { ws.WorkspaceDB, ws.Collaboration = db, board }()
	for _, method := range []string{"git.run", "memory.execute", "board.delete"} {
		if run, ok := a.prepareService(coreprotocol.Request{Method: method, Payload: json.RawMessage(`{}`)}); ok || run != nil {
			t.Fatalf("open dispatch for %s", method)
		}
	}
	for _, test := range []struct{ method, payload, code string }{
		{coreprotocol.GitSnapshot, `{"command":"rm"}`, "invalid_request"},
		{coreprotocol.SearchMemories, `{}`, "service_failed"},
		{coreprotocol.BoardSnapshot, `{}`, "service_failed"},
		{coreprotocol.PinMemory, `{"ID":"x","ExpectedRevision":1} {}`, "invalid_request"},
	} {
		run, ok := a.prepareService(coreprotocol.Request{ID: "request", Method: test.method, Payload: json.RawMessage(test.payload)})
		if !ok {
			t.Fatal("known operation absent", test.method)
		}
		out := run(context.Background())
		if out.Error == nil || out.Error.Code != test.code || out.ID != "request" {
			t.Fatalf("%s: %+v", test.method, out)
		}
	}
	for _, method := range []string{coreprotocol.GitStageReviewed, coreprotocol.GitCheckout, coreprotocol.DiscardGitReviewed, coreprotocol.CorrectMemory, coreprotocol.MarkBoardRead} {
		if !serviceMutation(method) {
			t.Fatal("mutation not journaled", method)
		}
	}
	for _, method := range []string{coreprotocol.GitSnapshot, coreprotocol.PreviewGitDiscard, coreprotocol.SearchMemories, coreprotocol.BoardSnapshot} {
		if serviceMutation(method) {
			t.Fatal("read classified as mutation", method)
		}
	}
}

func TestMemoryServiceCapturedStoreCASAndAudit(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	ctx := context.Background()
	first, err := ws.WorkspaceDB.Memory.Remember(ctx, memory.Record{Kind: memory.KindFact, Title: "Service audit", Content: "Exact original assertion"})
	if err != nil {
		t.Fatal(err)
	}
	a := &actor{ws: ws}
	prepare := func(method string, input any) func(context.Context) coreprotocol.Response {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		run, ok := a.prepareService(coreprotocol.Request{ID: "memory-operation", Method: method, Payload: raw})
		if !ok {
			t.Fatal("missing service", method)
		}
		return run
	}
	pin := prepare(coreprotocol.PinMemory, coreprotocol.PinMemoryRequest{ID: first.ID, ExpectedRevision: first.Revision, Pinned: true})
	// A prepared worker must not reread a replacement workspace DB pointer.
	db := ws.WorkspaceDB
	ws.WorkspaceDB = nil
	result := pin(ctx)
	ws.WorkspaceDB = db
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	result = prepare(coreprotocol.ForgetMemoryRecord, coreprotocol.ForgetMemoryRecordRequest{ID: first.ID, ExpectedRevision: first.Revision})(ctx)
	if result.Error == nil {
		t.Fatal("stale retirement accepted")
	}
	result = prepare(coreprotocol.InspectMemory, coreprotocol.InspectMemoryRequest{ID: first.ID})(ctx)
	var detail coreprotocol.InspectMemoryResult
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if err := json.Unmarshal(result.Payload, &detail); err != nil {
		t.Fatal(err)
	}
	if !detail.Detail.Record.Pinned || detail.Detail.Record.Revision != 2 || len(detail.Detail.Revisions) != 2 || detail.Detail.Record.Content != "Exact original assertion" {
		t.Fatalf("audit lost: %+v", detail)
	}
	result = prepare(coreprotocol.ForgetMemoryRecord, coreprotocol.ForgetMemoryRecordRequest{ID: first.ID, ExpectedRevision: 2})(ctx)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	result = prepare(coreprotocol.SearchMemories, coreprotocol.SearchMemoriesRequest{Query: memory.ListQuery{}})(ctx)
	var listed coreprotocol.SearchMemoriesResult
	if err := json.Unmarshal(result.Payload, &listed); err != nil {
		t.Fatal(err)
	}
	if result.Error != nil || len(listed.Records) != 0 {
		t.Fatalf("retired assertion recalled: %+v %+v", result, listed)
	}
}
