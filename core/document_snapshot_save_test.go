package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"crude/coreprotocol"
	"crude/workspace"
)

func TestDocumentSnapshotSaveExpectedPreimageAndCapturedAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "document")
	before := []byte("original")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	service := workspace.NewDocumentSaveService()
	t.Cleanup(service.Close)
	a := &actor{ws: &workspace.Workspace{Documents: service}}
	in := workspace.DocumentSaveRequest{BufferID: "buffer", Path: path, Data: []byte("replacement"), Revision: 19,
		ExpectedExist: true, ExpectedHash: sha256.Sum256(before), Reason: workspace.DocumentSaveManual}
	payload, err := coreprotocol.Marshal(coreprotocol.Ptr(coreprotocol.DocumentSnapshotSaveRequest{Request: in}))
	if err != nil {
		t.Fatal(err)
	}
	run, known := a.prepareDocumentSnapshotSave(coreprotocol.Request{Method: coreprotocol.DocumentSnapshotSave, Payload: payload})
	if !known || run == nil {
		t.Fatal("snapshot save not recognized")
	}
	// Preparation must not write; the worker must not dereference actor state.
	a.ws.Documents = nil
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("preparation changed disk: %q %v", data, err)
	}
	res := run(context.Background())
	if res.Error != nil {
		t.Fatal(res.Error)
	}
	var wire coreprotocol.DocumentSnapshotSaveResult
	if err := coreprotocol.Decode(res.Payload, &wire); err != nil {
		t.Fatal(err)
	}
	result := wire.DocumentResult()
	if result.Err != nil || !result.Committed || !reflect.DeepEqual(result.Request, in) || result.DiskHash != sha256.Sum256(in.Data) {
		t.Fatalf("incorrect save result: %+v", result)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("worker failed to commit: %q %v", data, err)
	}
	// Reusing the original disk precondition cannot overwrite newer bytes.
	res = run(context.Background())
	if err := coreprotocol.Decode(res.Payload, &wire); err != nil {
		t.Fatal(err)
	}
	result = wire.DocumentResult()
	if !errors.Is(result.Err, workspace.ErrDocumentConflict) || result.Committed {
		t.Fatalf("stale preimage accepted: %+v", result)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("conflict changed disk: %q %v", data, err)
	}
}

func TestDocumentSnapshotSaveResultSerialization(t *testing.T) {
	for _, original := range []workspace.DocumentSaveResult{
		{Committed: true, DiskHash: sha256.Sum256([]byte("saved")), Err: errors.New("replacement committed but durability failed")},
		{Unchanged: true},
		{Err: workspace.ErrDocumentConflict},
		{Err: errors.New("permission denied")},
	} {
		encoded, err := json.Marshal(coreprotocol.NewDocumentSnapshotSaveResult(original))
		if err != nil {
			t.Fatal(err)
		}
		var wire coreprotocol.DocumentSnapshotSaveResult
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatal(err)
		}
		got := wire.DocumentResult()
		if got.Committed != original.Committed || got.Unchanged != original.Unchanged || got.DiskHash != original.DiskHash || (got.Err == nil) != (original.Err == nil) || errors.Is(got.Err, workspace.ErrDocumentConflict) != errors.Is(original.Err, workspace.ErrDocumentConflict) {
			t.Fatalf("result classification lost: %+v => %+v", original, got)
		}
		if original.Err != nil && !wire.Conflict && got.Err.Error() != original.Err.Error() {
			t.Fatalf("error evidence lost: %v => %v", original.Err, got.Err)
		}
	}
}

func TestDocumentSnapshotSaveDoesNotHijackLiveSave(t *testing.T) {
	a := &actor{}
	if run, known := a.prepareDocumentSnapshotSave(coreprotocol.Request{Method: coreprotocol.DocumentSave}); known || run != nil || documentSnapshotSaveMutation(coreprotocol.DocumentSave) {
		t.Fatal("live shared-document save was hijacked")
	}
	if !documentSnapshotSaveMutation(coreprotocol.DocumentSnapshotSave) {
		t.Fatal("snapshot save missing mutation classification")
	}
	for _, payload := range []json.RawMessage{json.RawMessage(`{`), json.RawMessage(`{"request":{}}`)} {
		a.ws = &workspace.Workspace{}
		run, known := a.prepareDocumentSnapshotSave(coreprotocol.Request{Method: coreprotocol.DocumentSnapshotSave, Payload: payload})
		if !known || run(context.Background()).Error == nil {
			t.Fatal("invalid request or missing authority did not fail closed")
		}
	}
}
