package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"crude/coreprotocol"
	"crude/sessiondb"
	"crude/workspace"
)

func TestReferenceBridgeContentAndPreimages(t *testing.T) {
	store := workspace.NewReferenceStore("session", t.TempDir(), nil)
	value, err := runReferenceCommand(store, coreprotocol.ReferenceCreate, coreprotocol.ReferenceCommand{SessionID: "session", Create: &coreprotocol.ReferenceCreateRequest{Kind: sessiondb.KindPastedTextFile, Data: []byte("complete original"), Mime: "text/plain"}})
	if err != nil {
		t.Fatal(err)
	}
	view := value.(coreprotocol.ReferenceView)
	in := coreprotocol.ReferenceCommand{SessionID: "session", ID: view.Reference.ID, Revision: view.Revision}
	value, err = runReferenceCommand(store, coreprotocol.ReferenceReadContent, in)
	if err != nil {
		t.Fatal(err)
	}
	read := value.(coreprotocol.ReferenceContentResult)
	if string(read.Data) != "complete original" || read.ReadOnly {
		t.Fatalf("bad content: %+v", read)
	}
	in.Original, in.Content = "wrong original", "replacement"
	if _, err = runReferenceCommand(store, coreprotocol.ReferenceSaveContent, in); err == nil {
		t.Fatal("accepted wrong original")
	}
	in.Original = "complete original"
	value, err = runReferenceCommand(store, coreprotocol.ReferenceSaveContent, in)
	if err != nil {
		t.Fatal(err)
	}
	updated := value.(coreprotocol.ReferenceView)
	if updated.Revision == view.Revision { // file-backed content lives outside metadata; byte preimage still fences saves.
		in.Original = "complete original"
		if _, err = runReferenceCommand(store, coreprotocol.ReferenceSaveContent, in); err == nil {
			t.Fatal("accepted stale file bytes")
		}
	}
	in.Revision, in.Label = updated.Revision, "renamed"
	value, err = runReferenceCommand(store, coreprotocol.ReferenceRename, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runReferenceCommand(store, coreprotocol.ReferenceDelete, in); err == nil {
		t.Fatal("accepted stale metadata")
	}
	current := value.(coreprotocol.ReferenceView)
	in.Revision = current.Revision
	if _, err = runReferenceCommand(store, coreprotocol.ReferenceDelete, in); err != nil {
		t.Fatal(err)
	}
	if _, err = runReferenceCommand(store, coreprotocol.ReferenceReadContent, in); err == nil {
		t.Fatal("read deleted reference")
	}
}

func TestReferenceBridgeChunkedMediaAndIsolation(t *testing.T) {
	store := workspace.NewReferenceStore("session", t.TempDir(), nil)
	data := bytes.Repeat([]byte{0, 1, 255, 3}, coreprotocol.ReferenceChunkBytes)
	value, err := runReferenceCommand(store, coreprotocol.ReferenceCreate, coreprotocol.ReferenceCommand{SessionID: "session", Create: &coreprotocol.ReferenceCreateRequest{Kind: sessiondb.KindPastedImage, Data: data, Mime: "image/png"}})
	if err != nil {
		t.Fatal(err)
	}
	view := value.(coreprotocol.ReferenceView)
	if view.Reference.Content != "" || view.Reference.FilePath == "" {
		t.Fatal("image was not server-owned")
	}
	in := coreprotocol.ReferenceCommand{SessionID: "other", ID: view.Reference.ID}
	if _, err = runReferenceCommand(store, coreprotocol.ReferenceReadMedia, in); err == nil {
		t.Fatal("cross-session media accepted")
	}
	in.SessionID = "session"
	var downloaded []byte
	for {
		value, err = runReferenceCommand(store, coreprotocol.ReferenceReadMedia, in)
		if err != nil {
			t.Fatal(err)
		}
		chunk := value.(coreprotocol.ReferenceContentResult)
		if len(chunk.Data) > coreprotocol.ReferenceChunkBytes {
			t.Fatal("unbounded response")
		}
		downloaded = append(downloaded, chunk.Data...)
		if chunk.Next == chunk.Total {
			break
		}
		in.Offset, in.Digest = chunk.Next, chunk.Digest
	}
	if !bytes.Equal(data, downloaded) {
		t.Fatal("media truncated")
	}
	in.Digest = strings.Repeat("0", 64)
	if _, err = runReferenceCommand(store, coreprotocol.ReferenceReadMedia, in); err == nil {
		t.Fatal("accepted changed digest")
	}
	value, err = runReferenceCommand(store, coreprotocol.ReferenceList, coreprotocol.ReferenceCommand{SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	projection := value.(coreprotocol.ReferenceListResult)
	projection.References[0].Reference.Label = "client mutation"
	actual, _ := store.GetReference(view.Reference.ID)
	if actual.Label == "client mutation" {
		t.Fatal("projection mutates core")
	}
	if err = os.Remove(actual.FilePath); err != nil {
		t.Fatal(err)
	}
	in.Offset, in.Digest = 0, ""
	if _, err = runReferenceCommand(store, coreprotocol.ReferenceReadMedia, in); err == nil {
		t.Fatal("missing media silently downgraded")
	}
}

func TestPrepareReferencesFailsClosed(t *testing.T) {
	a := &actor{ws: &workspace.Workspace{}}
	raw, err := json.Marshal(coreprotocol.ReferenceCommand{SessionID: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	run, known := a.prepareReferences(coreprotocol.Request{Method: coreprotocol.ReferenceList, Payload: raw})
	if !known || run(context.Background()).Error.Code != "session_missing" {
		t.Fatal("missing owner not rejected")
	}
	run, known = a.prepareReferences(coreprotocol.Request{Method: coreprotocol.HistoryList})
	if !known || run(context.Background()).Error.Code != "history_unavailable" {
		t.Fatal("invented history writer")
	}
}
