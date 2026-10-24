package core

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/sessiondb"
	"crude/types"
	"crude/workspace"
)

func bulkRuntimeClient(t *testing.T) (*Runtime, *coreclient.Client, string, string) {
	t.Helper()
	setCoreTestHome(t, t.TempDir())
	root := t.TempDir()
	r, err := New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "bulk-test", types.Settings{}, coreTestLogger{})
		ws.Sessions = append(ws.Sessions, &workspace.Session{SessionID: "bulk-session", RefStore: workspace.NewReferenceStore("bulk-session", t.TempDir(), nil)})
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	a, b := net.Pipe()
	go func() { _ = ServeConnection(t.Context(), r, b) }()
	client, err := coreclient.Dial(t.Context(), coreclient.SingleCarrier(a), "bulk-client")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var attached coreprotocol.AttachResult
	if err := client.Call(t.Context(), "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	return r, client, root, attached.WorkspaceID
}

func TestBulkFileAndReferenceProductJourney(t *testing.T) {
	_, client, root, id := bulkRuntimeClient(t)
	data := bytes.Repeat([]byte("large verified bytes\n"), 500000)
	file := filepath.Join(root, "large.txt")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := client.ReadClientFile(t.Context(), id, "large.txt", false)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("large file truncated", len(got), err)
	}
	for _, path := range []string{"../outside", filepath.Join(t.TempDir(), "outside")} {
		if data, err := client.ReadClientFile(t.Context(), id, path, false); err == nil || data != nil {
			t.Fatal("escaped project authority")
		}
	}
	if err := os.Symlink(file, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if got, err := client.ReadClientFile(t.Context(), id, "link", false); err == nil || got != nil {
		t.Fatal("symlink read accepted")
	}
	outside := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(outside, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := client.ReadClientFile(t.Context(), id, outside, true); err != nil || string(got) != "external" {
		t.Fatal("explicit external read failed", err)
	}
	view := &coreclient.WorkspaceView{Client: client, ID: id}
	ref, err := view.CreateReference(t.Context(), "bulk-session", coreprotocol.ReferenceCreateRequest{Kind: sessiondb.KindPastedTextFile, Data: data})
	if err != nil {
		t.Fatal("upload", err)
	}
	read, err := view.ReadReferenceContent(t.Context(), "bulk-session", ref.Reference.ID)
	if err != nil || !bytes.Equal(read.Data, data) {
		t.Fatal("reference download", err)
	}
	ref, err = view.SaveReferenceContent(t.Context(), "bulk-session", ref, string(data), "replacement")
	if err != nil {
		t.Fatal("replacement", err)
	}
	read, err = view.ReadReferenceContent(t.Context(), "bulk-session", ref.Reference.ID)
	if err != nil || string(read.Data) != "replacement" {
		t.Fatal("replacement not published", err)
	}
	if _, err = view.SaveReferenceContent(t.Context(), "bulk-session", ref, "stale", "overwrite"); err == nil {
		t.Fatal("stale preimage accepted")
	}
	list, err := view.ListReferences(t.Context(), "bulk-session")
	if err != nil || len(list.References) != 1 {
		t.Fatal("upload published duplicate reference", err)
	}
}

func TestBulkUploadRejectsReplayAndDigestWithoutPublication(t *testing.T) {
	r, client, _, id := bulkRuntimeClient(t)
	// Open the carrier directly so deliberately invalid chunks use the real
	// server entry point, without deriving expectations from its helpers.
	r.mu.Lock()
	connection := r.clients[client.Owner().ClientID]
	r.mu.Unlock()
	for _, badDigest := range []bool{false, true} {
		a, b := net.Pipe()
		server := corewire.NewMux(b, corewire.CarrierLocal, false)
		peer := corewire.NewMux(a, corewire.CarrierLocal, true)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		s, err := peer.OpenStream(ctx, corewire.ClassBulk)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			stream, err := server.AcceptStream(ctx)
			if err == nil {
				connection.serveStream(ctx, stream)
			}
		}()
		in := coreprotocol.ReferenceUploadCommand{SessionID: "bulk-session", Purpose: coreprotocol.ReferenceUploadCreate, Size: 3, Digest: contentDigest([]byte("yes")), Create: &coreprotocol.ReferenceCreateRequest{Kind: sessiondb.KindPastedTextFile}}
		metadata, _ := coreprotocol.Marshal(&in)
		_ = send(s, &coreprotocol.Open{Kind: coreprotocol.StreamBulk, ID: "bad-upload", WorkspaceID: id, Method: coreprotocol.ReferenceUploadBegin})
		_ = send(s, &coreprotocol.BulkStart{Size: 3, Digest: in.Digest, Metadata: metadata})
		reader := corewire.NewReader(s, coreprotocol.BulkMessageBytes)
		body, err := reader.Next()
		if err != nil {
			t.Fatal(err)
		}
		var ready coreprotocol.BulkReady
		if err := coreprotocol.Decode(body, &ready); err != nil || ready.Fault != nil {
			t.Fatal("admission failed", err, ready.Fault)
		}
		chunk, _ := coreprotocol.Marshal(&coreprotocol.BulkHeader{Seq: 1})
		_ = corewire.WriteMessage(s, append(chunk, []byte("bad")...))
		if badDigest {
			_ = send(s, &coreprotocol.BulkHeader{Seq: 2, Offset: 3, Final: true, Digest: in.Digest})
		} else {
			_ = corewire.WriteMessage(s, append(chunk, []byte("bad")...))
		}
		body, err = reader.Next()
		if err != nil {
			t.Fatal(err)
		}
		var failed coreprotocol.BulkHeader
		if err := coreprotocol.Decode(body, &failed); err != nil || failed.Fault == nil {
			t.Fatal("invalid stream accepted", err)
		}
		_ = send(s, &coreprotocol.TopicAck{})
		_ = s.Close()
		_ = peer.Close()
		_ = server.Close()
		cancel()
		<-done
	}
	list, err := (&coreclient.WorkspaceView{Client: client, ID: id}).ListReferences(t.Context(), "bulk-session")
	if err != nil || len(list.References) != 0 {
		t.Fatal("partial upload published", err)
	}
}

func TestBulkStalledReaderDoesNotBlockInput(t *testing.T) {
	r, client, root, id := bulkRuntimeClient(t)
	if err := os.WriteFile(filepath.Join(root, "large"), bytes.Repeat([]byte("x"), 16<<20), 0600); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	connection := r.clients[client.Owner().ClientID]
	r.mu.Unlock()
	a, b := net.Pipe()
	server, peer := corewire.NewMux(b, corewire.CarrierLocal, false), corewire.NewMux(a, corewire.CarrierLocal, true)
	defer server.Close()
	defer peer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			s, err := server.AcceptStream(ctx)
			if err != nil {
				return
			}
			go connection.serveStream(ctx, s)
		}
	}()
	bulk, err := peer.OpenStream(ctx, corewire.ClassBulk)
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := coreprotocol.Marshal(&coreprotocol.FilePathRequest{Path: "large"})
	_ = send(bulk, &coreprotocol.Open{Kind: coreprotocol.StreamBulk, ID: "stalled", WorkspaceID: id, Method: coreprotocol.FilesRead})
	_ = send(bulk, &coreprotocol.BulkStart{Metadata: metadata})
	// Do not consume any bulk bytes: its stream credit must stall only itself.
	input, err := peer.OpenStream(ctx, corewire.ClassInput)
	if err != nil {
		t.Fatal(err)
	}
	_ = send(input, &coreprotocol.Open{Kind: coreprotocol.StreamCommand, ID: "input-priority", WorkspaceID: id, Method: coreprotocol.StatusSnapshotMethod}, &coreprotocol.Empty{})
	body, err := corewire.NewReader(input, corewire.MaxMessage).Next()
	if err != nil {
		t.Fatal("input blocked behind bulk", err)
	}
	var reply coreprotocol.Reply
	if _, err := coreprotocol.DecodePrefix(body, &reply); err != nil || reply.Fault != nil || !reply.Final {
		t.Fatal("input did not reach independent route", err)
	}
	_ = send(input, &coreprotocol.TopicAck{Seq: 1})
	_ = input.Close()
	_ = bulk.Close()
	cancel()
	_ = peer.Close()
	<-done
}

func TestCheckedFileRejectsReplacementAndCancellation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	_ = os.WriteFile(path, []byte("before"), 0600)
	f, err := openCheckedFile(root, path, coreprotocol.MaxFileRead)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_ = os.Rename(path, path+".old")
	_ = os.WriteFile(path, []byte("before"), 0600)
	if f.verify() == nil {
		t.Fatal("replacement inode accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := readFileChunk(ctx, root, coreprotocol.FileReadRequest{Path: path}); err == nil {
		t.Fatal("cancelled read succeeded")
	}
	_, _ = io.Copy(io.Discard, f)
}
