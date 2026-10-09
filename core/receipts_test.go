package core

import (
	"context"
	"crude/coreprotocol"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReceiptExclusiveRecoveryAndReuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.db")
	journal, err := openReceipts(path)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := openReceipts(path); err == nil {
		_ = other.close()
		t.Fatal("concurrent journal owner accepted")
	}
	request := coreprotocol.Request{ID: "one", Owner: coreprotocol.Owner{ClientID: "client", Epoch: 1}, WorkspaceID: "workspace", Method: coreprotocol.SubmitInput, Payload: json.RawMessage(`{"text":"hello"}`)}
	if _, fresh, err := journal.begin(context.Background(), request); err != nil || !fresh {
		t.Fatalf("begin: %v %v", fresh, err)
	}
	if err := journal.close(); err != nil {
		t.Fatal(err)
	}
	journal, err = openReceipts(path)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.close()
	request.Owner.Epoch++
	prior, fresh, err := journal.begin(context.Background(), request)
	if err != nil || fresh || prior.State != "uncertain" {
		t.Fatalf("crash replay: %+v %v %v", prior, fresh, err)
	}
	request.ID = "two"
	if _, fresh, err := journal.begin(context.Background(), request); err != nil || !fresh {
		t.Fatal(err)
	}
	out := response(request, coreprotocol.DecisionResult{Accepted: true})
	if err := journal.complete(context.Background(), request, out); err != nil {
		t.Fatal(err)
	}
	prior, fresh, err = journal.begin(context.Background(), request)
	if err != nil || fresh || prior.State != "completed" || string(prior.Response.Payload) != string(out.Payload) {
		t.Fatalf("lost response: %+v %v %v", prior, fresh, err)
	}
	request.Payload = json.RawMessage(`{"text":"different"}`)
	if _, _, err := journal.begin(context.Background(), request); err == nil {
		t.Fatal("request reuse accepted")
	}
}

func TestReceiptRejectsSymlinkAndPublicFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if journal, err := openReceipts(link); err == nil {
		_ = journal.close()
		t.Fatal("symlink accepted")
	}
	if err := os.Chmod(target, 0644); err != nil {
		t.Fatal(err)
	}
	if journal, err := openReceipts(target); err == nil {
		_ = journal.close()
		t.Fatal("public file accepted")
	}
}
