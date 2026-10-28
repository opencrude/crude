package core

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/coreprotocol"
	"crude/workspace"
)

func TestStartupCreateFolderUsesDurableReceiptAndNoWorkspace(t *testing.T) {
	parent := t.TempDir()
	r, err := New(func(string) (*workspace.Workspace, error) {
		t.Error("folder creation constructed a workspace")
		return nil, errors.New("unexpected workspace")
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	c, err := r.Connect("folder-creator")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	input := coreprotocol.StartupCreateFolderRequest{Parent: parent, Name: "project"}
	first := callCore(t, c, "create-once", "", coreprotocol.StartupCreateFolder, input)
	var created coreprotocol.StartupCreateFolderResult
	if err := coreprotocol.Decode(first.Payload, &created); err != nil || created.Path != filepath.Join(parent, "project") {
		t.Fatalf("creation result=%+v err=%v", created, err)
	}
	if err := os.WriteFile(filepath.Join(created.Path, "keep"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	replayed := callCore(t, c, "create-once", "", coreprotocol.StartupCreateFolder, input)
	if !bytes.Equal(first.Payload, replayed.Payload) {
		t.Fatal("same request identity did not return the original result")
	}
	var receipt coreprotocol.Receipt
	decodeResource(t, callCore(t, c, "observe", "", coreprotocol.GetReceipt, coreprotocol.ReceiptRequest{RequestID: "create-once"}), &receipt)
	if receipt.State != "completed" || receipt.Method != coreprotocol.StartupCreateFolder {
		t.Fatalf("missing durable receipt: %+v", receipt)
	}
	for _, tc := range []struct {
		id   string
		name string
	}{
		{"create-once", "different"},
		{"collision", "project"},
		{"escape", "../escaped"},
	} {
		raw, err := coreprotocol.Marshal(&coreprotocol.StartupCreateFolderRequest{Parent: parent, Name: tc.name})
		if err != nil {
			t.Fatal(err)
		}
		result := c.Call(t.Context(), coreprotocol.Request{ID: tc.id, Owner: c.Owner, Method: coreprotocol.StartupCreateFolder, Payload: raw})
		if result.Error == nil {
			t.Fatalf("accepted %q under request %q", tc.name, tc.id)
		}
	}
	if data, err := os.ReadFile(filepath.Join(created.Path, "keep")); err != nil || string(data) != "safe" {
		t.Fatalf("existing content changed: %q %v", data, err)
	}
	if len(r.workspaces) != 0 || len(r.creating) != 0 {
		t.Fatal("creation attached project resources")
	}
}
