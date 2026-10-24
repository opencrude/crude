package core

import (
	"bytes"
	"context"
	"os"
	"testing"

	"crude/coreprotocol"
	"crude/sessiondb"
	"crude/workspace"
)

func TestReferenceUploadFullSizeAndPreimage(t *testing.T) {
	root := t.TempDir()
	m := NewReferenceUploads(root)
	t.Cleanup(func() { _ = m.Close() })
	s := workspace.NewReferenceStore("s", t.TempDir(), nil)
	owner := coreprotocol.Owner{ClientID: "client", Epoch: 1}
	run := func(method string, in coreprotocol.ReferenceUploadCommand) (any, error) {
		return m.run(context.Background(), owner, "workspace", s, method, in)
	}
	data := bytes.Repeat([]byte("abc\n"), (9<<20)/4)
	begin := coreprotocol.ReferenceUploadCommand{SessionID: "s", Purpose: coreprotocol.ReferenceUploadCreate, Size: int64(len(data)), Digest: contentDigest(data), Create: &coreprotocol.ReferenceCreateRequest{Kind: sessiondb.KindPastedTextFile}}
	value, err := run(coreprotocol.ReferenceUploadBegin, begin)
	if err != nil {
		t.Fatal(err)
	}
	id := value.(coreprotocol.ReferenceUploadResult).UploadID
	chunk := coreprotocol.ReferenceUploadCommand{SessionID: "s", UploadID: id, Data: data[:coreprotocol.ReferenceChunkBytes]}
	if _, err = run(coreprotocol.ReferenceUploadChunk, chunk); err != nil {
		t.Fatal(err)
	}
	if _, err = run(coreprotocol.ReferenceUploadChunk, chunk); err == nil {
		t.Fatal("replayed chunk appended twice")
	}
	for offset := coreprotocol.ReferenceChunkBytes; offset < len(data); offset += coreprotocol.ReferenceChunkBytes {
		chunk.Offset = int64(offset)
		chunk.Data = data[offset:min(offset+coreprotocol.ReferenceChunkBytes, len(data))]
		if _, err = run(coreprotocol.ReferenceUploadChunk, chunk); err != nil {
			t.Fatal(err)
		}
	}
	value, err = run(coreprotocol.ReferenceUploadCommit, coreprotocol.ReferenceUploadCommand{SessionID: "s", UploadID: id})
	if err != nil {
		t.Fatal(err)
	}
	view := value.(*coreprotocol.ReferenceView)
	actual, err := os.ReadFile(view.Reference.FilePath)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("large transfer truncated", err)
	}
	if _, err = run(coreprotocol.ReferenceUploadCommit, coreprotocol.ReferenceUploadCommand{SessionID: "s", UploadID: id}); err == nil {
		t.Fatal("commit replay created another reference")
	}
	begin = coreprotocol.ReferenceUploadCommand{SessionID: "s", Purpose: coreprotocol.ReferenceUploadReplace, ID: view.Reference.ID, Revision: view.Revision, OriginalDigest: contentDigest(data), Size: 3, Digest: contentDigest([]byte("new"))}
	value, err = run(coreprotocol.ReferenceUploadBegin, begin)
	if err != nil {
		t.Fatal(err)
	}
	id = value.(coreprotocol.ReferenceUploadResult).UploadID
	if _, err = run(coreprotocol.ReferenceUploadChunk, coreprotocol.ReferenceUploadCommand{SessionID: "s", UploadID: id, Data: []byte("new")}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(view.Reference.FilePath, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = run(coreprotocol.ReferenceUploadCommit, coreprotocol.ReferenceUploadCommand{SessionID: "s", UploadID: id}); err == nil {
		t.Fatal("stale original accepted")
	}
	actual, err = os.ReadFile(view.Reference.FilePath)
	if err != nil || string(actual) != "external" {
		t.Fatal("stale edit overwrote external bytes")
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 {
		t.Fatal("commit leaked staging files", err)
	}
}

func TestReferenceUploadQuotaDigestDetach(t *testing.T) {
	root := t.TempDir()
	m := NewReferenceUploads(root)
	s := workspace.NewReferenceStore("s", t.TempDir(), nil)
	o := coreprotocol.Owner{ClientID: "client", Epoch: 1}
	begin := coreprotocol.ReferenceUploadCommand{SessionID: "s", Purpose: coreprotocol.ReferenceUploadCreate, Size: 3, Digest: contentDigest([]byte("yes")), Create: &coreprotocol.ReferenceCreateRequest{Kind: sessiondb.KindPastedTextFile}}
	first, err := m.run(context.Background(), o, "w", s, coreprotocol.ReferenceUploadBegin, begin)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.run(context.Background(), o, "w", s, coreprotocol.ReferenceUploadBegin, begin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.run(context.Background(), o, "w", s, coreprotocol.ReferenceUploadBegin, begin); err == nil {
		t.Fatal("quota not enforced")
	}
	id := first.(coreprotocol.ReferenceUploadResult).UploadID
	command := coreprotocol.ReferenceUploadCommand{SessionID: "s", UploadID: id, Data: []byte("bad")}
	foreign := o
	foreign.Epoch++
	if _, err = m.run(context.Background(), foreign, "w", s, coreprotocol.ReferenceUploadChunk, command); err == nil {
		t.Fatal("foreign epoch accepted")
	}
	if _, err = m.run(context.Background(), o, "w", s, coreprotocol.ReferenceUploadChunk, command); err != nil {
		t.Fatal(err)
	}
	command.Data = nil
	if _, err = m.run(context.Background(), o, "w", s, coreprotocol.ReferenceUploadCommit, command); err == nil {
		t.Fatal("digest mismatch accepted")
	}
	if len(s.ListReferences()) != 0 {
		t.Fatal("bad digest published reference")
	}
	m.Detach(o, "w")
	if _, err = m.run(context.Background(), o, "w", s, coreprotocol.ReferenceUploadBegin, begin); err == nil {
		t.Fatal("delayed detached begin accepted")
	}
	command.UploadID = second.(coreprotocol.ReferenceUploadResult).UploadID
	if _, err = m.run(context.Background(), o, "w", s, coreprotocol.ReferenceUploadCommit, command); err == nil {
		t.Fatal("detached commit accepted")
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 {
		t.Fatal("detach leaked files", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}
