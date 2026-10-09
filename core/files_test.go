package core

import (
	"bytes"
	"context"
	"crude/coreprotocol"
	"crude/localhistory"
	"crude/types"
	"crude/workspace"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func fileBridge(t *testing.T, a *actor, method string, in any) coreprotocol.Response {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	run, known := a.prepareFiles(coreprotocol.Request{Method: method, Payload: raw})
	if !known {
		t.Fatal("unknown method", method)
	}
	return run(context.Background())
}
func TestFilesChunksBoundedAndImmutable(t *testing.T) {
	root := t.TempDir()
	data := bytes.Repeat([]byte("abcdef"), 100000)
	path := filepath.Join(root, "data")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	first, err := readFileChunk(context.Background(), root, coreprotocol.FileReadRequest{Path: "data"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != coreprotocol.MaxFileChunk || first.EOF || !bytes.Equal(first.Data, data[:coreprotocol.MaxFileChunk]) {
		t.Fatal("bad first chunk")
	}
	next, err := readFileChunk(context.Background(), root, coreprotocol.FileReadRequest{Path: "data", Offset: first.NextOffset, Digest: first.Digest})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(next.Data, data[first.NextOffset:next.NextOffset]) {
		t.Fatal("bad continuation")
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), len(data)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFileChunk(context.Background(), root, coreprotocol.FileReadRequest{Path: "data", Offset: first.NextOffset, Digest: first.Digest}); err == nil {
		t.Fatal("changed content accepted")
	}
	if _, err := readFileChunk(context.Background(), root, coreprotocol.FileReadRequest{Path: "../outside"}); err == nil {
		t.Fatal("escape accepted")
	}
	if _, err := readFileChunk(context.Background(), root, coreprotocol.FileReadRequest{Path: "data", Limit: coreprotocol.MaxFileChunk + 1}); err == nil {
		t.Fatal("oversized chunk accepted")
	}
	if err := os.Symlink(path, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := readFileChunk(context.Background(), root, coreprotocol.FileReadRequest{Path: "link"}); err == nil {
		t.Fatal("symlink accepted")
	}
	f, err := os.Create(filepath.Join(root, "large"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(coreprotocol.MaxFileRead + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readFileChunk(context.Background(), root, coreprotocol.FileReadRequest{Path: "large"}); err == nil {
		t.Fatal("oversized file accepted")
	}
}
func TestFilesBridgeMutationsAndCapturedOwner(t *testing.T) {
	root := t.TempDir()
	ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	a := &actor{ws: ws}
	for _, method := range []string{coreprotocol.FilesCreateFolder, coreprotocol.FilesCreate} {
		path := "dir"
		if method == coreprotocol.FilesCreate {
			path = "dir/file"
		}
		got := fileBridge(t, a, method, coreprotocol.FilePathRequest{Path: path})
		if got.Error != nil {
			t.Fatal(got.Error)
		}
	}
	got := fileBridge(t, a, coreprotocol.FilesEdit, coreprotocol.FileEditsRequest{Edits: []workspace.FileEdit{{Path: "dir/file", OldContent: "", NewContent: "hello"}}})
	if got.Error != nil {
		t.Fatal(got.Error)
	}
	got = fileBridge(t, a, coreprotocol.FilesEdit, coreprotocol.FileEditsRequest{Edits: []workspace.FileEdit{{Path: "dir/file", OldContent: "wrong", NewContent: "overwrite"}}})
	var failed coreprotocol.FileOperationResult
	if err := json.Unmarshal(got.Payload, &failed); err != nil {
		t.Fatal(err)
	}
	if failed.Error == "" {
		t.Fatal("preimage mismatch accepted")
	}
	data, err := os.ReadFile(filepath.Join(root, "dir/file"))
	if err != nil || string(data) != "hello" {
		t.Fatal("failed edit changed bytes", err)
	}
	raw, _ := json.Marshal(coreprotocol.FileReadRequest{Path: "dir/file"})
	run, _ := a.prepareFiles(coreprotocol.Request{Method: coreprotocol.FilesRead, Payload: raw})
	ws.RootPath = t.TempDir()
	if result := run(context.Background()); result.Error != nil {
		t.Fatal("captured root lost", result.Error)
	}
	ws.RootPath = root
	if got := fileBridge(t, a, coreprotocol.FilesDelete, coreprotocol.FilePathRequest{Path: root}); got.Error == nil {
		t.Fatal("root deletion allowed")
	}
	if got := fileBridge(t, a, coreprotocol.FilesRename, coreprotocol.FilePathRequest{Path: "dir/file", NewPath: "dir/renamed"}); got.Error != nil {
		t.Fatal(got.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "dir/renamed")); err != nil {
		t.Fatal(err)
	}
}
func TestFilesHistoryCaptureAndStalePreview(t *testing.T) {
	root := t.TempDir()
	ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	store, err := localhistory.Open(filepath.Join(t.TempDir(), "history"), root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ws.LocalHistory = store
	a := &actor{ws: ws}
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	got := fileBridge(t, a, coreprotocol.HistoryCapture, coreprotocol.HistoryCaptureRequest{})
	if got.Error != nil {
		t.Fatal(got.Error)
	}
	var snapshot localhistory.Manifest
	if err := json.Unmarshal(got.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	got = fileBridge(t, a, coreprotocol.HistoryPlan, coreprotocol.HistoryPlanRequest{SnapshotID: snapshot.ID})
	if got.Error != nil {
		t.Fatal(got.Error)
	}
	var plan localhistory.RestorePlan
	if err := json.Unmarshal(got.Payload, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 {
		t.Fatal("preview missing actual change")
	}
	if err := os.WriteFile(path, []byte("later"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyRestore(context.Background(), plan.ID, []string{"file"}); err == nil {
		t.Fatal("stale preview applied")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "later" {
		t.Fatal("stale preview lost later work")
	}
	got = fileBridge(t, a, coreprotocol.HistoryApply, coreprotocol.HistoryApplyRequest{PlanID: plan.ID})
	if got.Error == nil || got.Error.Code != "ownership_blocked" {
		t.Fatal("unleased restore exposed")
	}
	ws.LocalHistory = nil
}

func TestFilesSearchCancellationDoesNotInvalidateNeighbor(t *testing.T) {
	root := t.TempDir()
	ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	if err := os.WriteFile(filepath.Join(root, "needle.txt"), []byte("needle in text"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &actor{ws: ws}
	raw, err := json.Marshal(coreprotocol.FileSearchRequest{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	one, _ := a.prepareFiles(coreprotocol.Request{Method: coreprotocol.FilesSearch, Payload: raw})
	two, _ := a.prepareFiles(coreprotocol.Request{Method: coreprotocol.FilesSearch, Payload: raw})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := one(ctx); got.Error == nil {
		t.Fatal("cancelled request succeeded")
	}
	ws.CancelProjectSearch()
	got := two(context.Background())
	if got.Error != nil {
		t.Fatal("global search cancellation touched request-owned search", got.Error)
	}
	var out coreprotocol.FileSearchResult
	if err := json.Unmarshal(got.Payload, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) == 0 || out.Matches[0].RelativePath != "needle.txt" {
		t.Fatal("real search result missing", out)
	}
}
